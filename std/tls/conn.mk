// Why: after the handshake, a TLS connection must let one task read while
// another writes, survive a cancelled Read, and fail cleanly, all over one
// transport.
//
// What: tls.Conn (Read, WriteAll, Shutdown, Close, Abandon) for TLS 1.3 and
// TLS 1.2 (whose records record12.mk seals and opens), its read and write
// halves, and the handling of alerts, NewSessionTicket (read and dropped:
// no resumption) and KeyUpdate.
//
// How: RFC 8446 sections 5 and 6 for records and alerts, 4.6.1 and 4.6.3.
// The state lives on the heap the connection was made with, under three
// locks (rd, wr, sh) and these invariants:
// - L1 (lock order): rd, then wr, then sh. A task holding wr never takes
//   rd; sh is taken last and never held across I/O, so no two tasks wait
//   on each other's lock.
// - L2 (halves): one Read at a time holds rd for the whole call, one
//   WriteAll or Close holds wr for the whole call, so a Read and a
//   WriteAll run beside each other in two tasks and the records of one
//   WriteAll are never interleaved with another's.
// - L3 (repeatable Read): every byte read from the transport and not yet
//   given to a caller stays in rd (a partial record in buf, an opened
//   record's rest in plain, a partial post-handshake message in hs), so a
//   Read interrupted by a cancel or a deadline loses nothing and the next
//   Read continues.
// - L4 (failed): an error of a WriteAll or Close (a cancel included), or
//   any error but Canceled of a Read, stores the error in sh.fault and
//   shuts the transport down; every later call but Abandon fails with that
//   error, and a Read or WriteAll blocked on the transport in another task
//   returns at once, as the shut-down transport answers it.
// - L5 (owed reply): a KeyUpdate that asks for one back sets sh.owe; the
//   next WriteAll or Close sends the KeyUpdate first, under wr, then moves
//   its own key forward (RFC 8446 4.6.3).
// - L6 (shutdown, as net.Transport requires): Shutdown marks the
//   directions it ends in sh before it shuts the transport, so a call it
//   wakes reads the mark: a Read woken after Shutdown(Read) or (Both)
//   returns 0 and a WriteAll woken after Shutdown(Write) or (Both) fails
//   with Closed, neither storing a failure. A WriteAll in flight (sh.writing,
//   set and read in one sh section with wshut) is not waited for: the
//   transport is shut at once and no close_notify is sent; otherwise
//   close_notify goes first, within the close limit.

import std.core
import std.crypto
import std.net
import std.task
import std.time
import std.x509

// ReadHalf is the receiving direction: its protection and application
// secret, the record bytes read ahead (buf[start..have]), the content of the
// record being given out (plain[at..end]), and a post-handshake message
// in progress (its header in head[0..head_n], or skip bytes of a
// NewSessionTicket left to drop), and whether it is a server's, which
// never takes a NewSessionTicket.
struct ReadHalf {
    aead: Aead
    secret: Secret
    buf: [RecordMax]u8
    start: int
    have: int
    plain: [MaxSealed]u8
    at: int
    end: int
    head: [4]u8
    head_n: int
    skip: int
    useless: int
    eof: bool
    server: bool
}

// WriteHalf is the sending direction: its protection and application
// secret, and the inner plaintext and record a record is built in.
struct WriteHalf {
    aead: Aead
    secret: Secret
    inner: [16385]u8
    buf: [RecordMax]u8
}

// Shared is what both halves read: the failure every later call reports,
// whether a KeyUpdate is owed to the peer, whether close_notify was sent,
// and the shutdown marks of L6: the read direction ended (rshut), the
// write direction ended for WriteAll (wshut), the transport's write side
// shut so nothing more is sent (down), and a WriteAll sending (writing).
struct Shared {
    fault: ?Error
    owe: bool
    notified: bool
    rshut: bool
    wshut: bool
    down: bool
    writing: bool
}

// Halves is a connection's state on its heap.
struct Halves {
    rd: Mutex[ReadHalf]
    wr: Mutex[WriteHalf]
    sh: Mutex[Shared]
    alpn: [255]u8
    alpn_len: int
    close_limit: time.Duration
}

// ReadHalf.New is a receiving direction with no keys.
ReadHalf.New(): ReadHalf {
    return ReadHalf(aead: Aead.Off(), secret: Secret(kind: crypto.HashKind.Sha256, b: [0; 48]), buf: [0; RecordMax], start: 0,
        have: 0, plain: [0; MaxSealed], at: 0, end: 0, head: [0; 4], head_n: 0, skip: 0, useless: 0,
        eof: false, server: false)
}

// WriteHalf.New is a sending direction with no keys.
WriteHalf.New(): WriteHalf {
    return WriteHalf(aead: Aead.Off(), secret: Secret(kind: crypto.HashKind.Sha256, b: [0; 48]), inner: [0; 16385],
        buf: [0; RecordMax])
}

// Halves.New is the state of a connection being made, closing within
// close_limit.
Halves.New(close_limit: time.Duration): Halves {
    return Halves(rd: Mutex[ReadHalf].New(ReadHalf.New()), wr: Mutex[WriteHalf].New(WriteHalf.New()),
        sh: Mutex[Shared].New(Shared(fault: none, owe: false, notified: false, rshut: false, wshut: false, down: false,
            writing: false)), alpn: [0; 255], alpn_len: 0,
        close_limit: close_limit)
}

// FreeHalves wipes every key and secret of the state in b and frees it.
FreeHalves(b: Box[Halves], h: &Heap) {
    Halves(rd, wr, sh, ..) := b.Take(h.Alloc())
    _ = sh.Into()
    var r := rd.Into()
    r.aead.Wipe()
    r.secret.Wipe()
    crypto.Wipe(mut r.plain[..])
    var w := wr.Into()
    w.aead.Wipe()
    w.secret.Wipe()
    crypto.Wipe(mut w.inner[..])
}

/// A TLS connection, made by Client or Server. It is linear, Send and
/// Share: tasks share it by reference, one reading while another writes,
/// since its read and write halves each have a lock. End it with Close or
/// Abandon.
///
/// Its state is on the heap given to Client or Server. It keeps that heap
/// and its connection, never its Config: it copies what it needs of the
/// Config and the host name, such as the chosen ALPN protocol, so it
/// outlives both.
export linear view struct Conn {
    conn: net.Conn
    heap: &Heap
    box: Box[Halves]
}

// Halves.Check fails with the stored failure once the connection failed
// (L4), and with nothing else: sh is held for a few loads, so its lock is
// taken with the cancellation masked, and a cancel is found at the half's
// lock, where the call's own class applies.
Halves.Check(&self, io: Io): Error!void {
    var g := self.sh.Lock(io.NoCancel()) else e {
        fail e
    }
    if f := g.Ref().fault {
        g.Unlock()
        fail f
    }
    g.Unlock()
}

// Halves.Fail stores e as the connection's failure, unless one is stored,
// sends the fatal alert code when it is not 0 (under wr, within the close
// limit, best effort: the connection is failing anyway), and shuts the
// transport down so a call blocked on it returns (L4). Returns the stored
// failure, which every call reports from now on.
Halves.Fail(&self, io: Io, conn: &net.Conn, e: Error, code: u8): Error {
    q := io.NoCancel()
    var g := self.sh.Lock(q) else {
        conn.Shutdown(q, net.Shutdown.Both) else {}
        return e
    }
    if g.Ref().fault.IsNone() {
        g.Mut().fault = some(e)
    }
    f := g.Ref().fault else { e }
    g.Unlock()
    if code != 0 {
        self.AlertWithin(conn, q, code)
    }
    conn.Shutdown(q, net.Shutdown.Both) else {}
    return f
}

// Halves.AlertWithin sends the fatal alert code under wr within the close
// limit, best effort: a WriteAll that holds wr past the limit keeps it,
// and the shutdown that follows wakes it.
Halves.AlertWithin(&self, conn: &net.Conn, q: Io, code: u8) {
    scope s := q.Scope() {
        s.CancelAfter(self.close_limit)
        var w := self.wr.Lock(q) else {
            return
        }
        SendAlert(w.Mut(), conn, q, code) else {}
        w.Unlock()
    }
}

// ReadHalf.Consume drops the first n bytes of the read-ahead; the rest
// stays where it is until Record needs the room.
ReadHalf.Consume(mut self, n: int) {
    self.start += n
    if self.start == self.have {
        self.start = 0
        self.have = 0
    }
}

// ReadHalf.Record reads until the read-ahead holds one whole record (RFC
// 8446 5.1) and returns its body's length; the record is buf[start..start
// + 5 + n]. A partial record is moved to the front only when the buffer's
// end is reached, so each byte is moved at most once. A body
// longer than the protection allows is record_overflow, a header whose
// type std.tls does not know is unexpected_message, and the transport's
// end before a whole record is Reset.
ReadHalf.Record(mut self, conn: &net.Conn, io: Io, alert: mut u8): Error!int {
    for {
        at := self.start
        if self.have - at >= 5 {
            ty := self.buf[at]
            if ty < TypeChangeCipherSpec || ty > TypeAppData || self.buf[at + 1] != 3 {
                *alert = AlertUnexpectedMessage
                fail Protocol
            }
            n := (int(self.buf[at + 3]) << 8) | int(self.buf[at + 4])
            limit := if self.aead.on { MaxSealed } else { MaxPlain }
            if n > limit {
                *alert = AlertRecordOverflow
                fail Protocol
            }
            if self.have - at >= 5 + n {
                return n
            }
        }
        if self.have == RecordMax {
            rest := self.have - at
            for i in 0..rest {
                self.buf[i] = self.buf[at + i]
            }
            self.start = 0
            self.have = rest
        }
        k := try conn.Read(io, mut self.buf[self.have..])
        if k == 0 {
            fail net.Reset
        }
        self.have += k
    }
}

// AlertOf reads an alert record's body (RFC 8446 6): a level and a
// description; returns the description.
AlertOf(body: []u8, alert: mut u8): Protocol!u8 {
    if body.Len() != 2 {
        *alert = AlertDecodeError
        fail Protocol
    }
    return body[1]
}

// ReadHalf.Rekey moves the read key to the next application secret (RFC
// 8446 7.2), wiping the old.
ReadHalf.Rekey(mut self) {
    var next := NextSecret(&self.secret)
    self.secret.Wipe()
    self.secret = next
    var k := TrafficKeyOf(self.aead.suite, &self.secret)
    self.aead.Wipe()
    self.aead = Aead.New(&k)
    k.Wipe()
}

// ReadHalf.PostHandshake reads the handshake content of one record after
// the handshake: a client drops NewSessionTicket bodies, KeyUpdate moves
// the read key and may owe a reply (L5); any other message, a
// NewSessionTicket read by a server among them (only a server sends one,
// RFC 8446 4.6.1, as Go's handlePostHandshakeMessage checks), is
// unexpected_message (4.6). A KeyUpdate must end its record (5.1: no
// message spans a key change).
ReadHalf.PostHandshake(mut self, sh: &Mutex[Shared], io: Io, n: int, alert: mut u8): Error!void {
    var i := 0
    for i < n {
        if self.skip > 0 {
            k := if self.skip < n - i { self.skip } else { n - i }
            self.skip -= k
            i += k
            continue
        }
        if self.head_n < 4 {
            self.head[self.head_n] = self.plain[i]
            self.head_n += 1
            i += 1
            if self.head_n == 4 {
                len := (int(self.head[1]) << 16) | (int(self.head[2]) << 8) | int(self.head[3])
                if self.head[0] == HsNewTicket && !self.server {
                    // RFC 8446 4.6.1's largest ticket: 13 bytes of fields,
                    // a 255-byte nonce, a 65,535-byte ticket and 65,534
                    // bytes of extensions.
                    if len < 13 || len > 131337 {
                        *alert = AlertDecodeError
                        fail Protocol
                    }
                    self.skip = len
                    self.head_n = 0
                } else if self.head[0] == HsKeyUpdate {
                    if len != 1 {
                        *alert = AlertDecodeError
                        fail Protocol
                    }
                } else {
                    *alert = AlertUnexpectedMessage
                    fail Protocol
                }
            }
            continue
        }
        b := self.plain[i]
        i += 1
        self.head_n = 0
        if b > 1 {
            *alert = AlertIllegalParameter
            fail Protocol
        }
        if i != n {
            *alert = AlertUnexpectedMessage
            fail Protocol
        }
        self.Rekey()
        if b == 1 {
            // Masked: the key moved, so the reply must not be lost to a
            // cancel (L5).
            var g := try sh.Lock(io.NoCancel())
            g.Mut().owe = true
            g.Unlock()
        }
    }
}

// MaxUseless is how many records in a row may deliver no application data
// (empty application data, NewSessionTicket, KeyUpdate) before Read
// refuses the peer: Go's crypto/tls's maxUselessRecords, 16. Without it a
// peer keeps one Read busy forever without sending a byte of data.
const MaxUseless: int = 16

// ReadHalf.NoData counts a record that delivered no application data;
// the 17th in a row is unexpected_message.
ReadHalf.NoData(mut self, alert: mut u8): Protocol!void {
    self.useless += 1
    if self.useless > MaxUseless {
        *alert = AlertUnexpectedMessage
        fail Protocol
    }
}

// ReadHalf.Data gives out application data: the rest of the opened
// record, else the next records, opened, until one carries data, the
// peer's close_notify (0, and every later Read 0), or a failure.
ReadHalf.Data(mut self, conn: &net.Conn, sh: &Mutex[Shared], io: Io, buf: mut []u8, alert: mut u8): Error!int {
    for {
        if self.at < self.end {
            k := if buf.Len() < self.end - self.at { buf.Len() } else { self.end - self.at }
            (mut buf[0..k]).CopyFrom(self.plain[self.at..self.at + k])
            self.at += k
            return k
        }
        if self.eof {
            return 0
        }
        n := try self.Record(conn, io, alert)
        if self.aead.v12 {
            try self.Take12(n, alert)
            continue
        }
        at := self.start
        if self.buf[at] != TypeAppData {
            // After the handshake every record is protected (RFC 8446 5):
            // a plaintext alert, a forged close_notify among them, is a
            // violation, never the end of the stream.
            *alert = AlertUnexpectedMessage
            fail Protocol
        }
        var head: [5]u8 = [0; 5]
        (mut head[..]).CopyFrom(self.buf[at..at + 5])
        r := try self.aead.Open(head[..], self.buf[at + 5..at + 5 + n], mut self.plain[..], alert)
        self.Consume(5 + n)
        if r.ty == TypeAppData {
            if self.head_n > 0 || self.skip > 0 {
                *alert = AlertUnexpectedMessage
                fail Protocol
            }
            if r.n > 0 {
                self.useless = 0
            } else {
                try self.NoData(alert)
            }
            self.at = 0
            self.end = r.n
        } else if r.ty == TypeHandshake {
            try self.NoData(alert)
            try self.PostHandshake(sh, io, r.n, alert)
        } else if r.ty == TypeAlert {
            code := try AlertOf(self.plain[0..r.n], alert)
            if code == AlertCloseNotify {
                self.eof = true
                return 0
            }
            fail Alert(code: code)
        } else {
            *alert = AlertUnexpectedMessage
            fail Protocol
        }
    }
}

// WriteHalf.Send writes data as records of type ty, each at most MaxPlain
// bytes, sealed when the direction has keys and plaintext otherwise (the
// first flight).
WriteHalf.Send(mut self, conn: &net.Conn, io: Io, ty: u8, data: []u8): Error!void {
    var i := 0
    for {
        k := if data.Len() - i < MaxPlain { data.Len() - i } else { MaxPlain }
        if self.aead.Worn() {
            try self.Renew(conn, io)
        }
        if self.aead.v12 {
            n := self.aead.Seal12(ty, data[i..i + k], mut self.buf[..])
            try conn.WriteAll(io, self.buf[0..n])
        } else if self.aead.on {
            n := self.aead.Seal(ty, data[i..i + k], mut self.inner[..], mut self.buf[..])
            try conn.WriteAll(io, self.buf[0..n])
        } else {
            RecordHeader(ty, k, mut self.buf[0..5])
            (mut self.buf[5..5 + k]).CopyFrom(data[i..i + k])
            try conn.WriteAll(io, self.buf[0..5 + k])
        }
        i += k
        if i >= data.Len() {
            return
        }
    }
}

// WriteHalf.Rekey moves the write key to the next application secret
// (RFC 8446 7.2), wiping the old.
WriteHalf.Rekey(mut self) {
    var next := NextSecret(&self.secret)
    self.secret.Wipe()
    self.secret = next
    var k := TrafficKeyOf(self.aead.suite, &self.secret)
    self.aead.Wipe()
    self.aead = Aead.New(&k)
    k.Wipe()
}

// WriteHalf.Renew moves the write key forward before it seals more
// records than RFC 8446 5.5 allows: a KeyUpdate that asks for none back,
// sealed under the old key's last sequence number, then the next key
// (4.6.3). Only an application key reaches its limit: a handshake key
// seals a few records. A TLS 1.2 key (2^64 - 1 records) is refused.
WriteHalf.Renew(mut self, conn: &net.Conn, io: Io): Error!void {
    if self.aead.v12 {
        // TLS 1.2 has no KeyUpdate: a key at its last record is spent.
        fail Protocol
    }
    var m: [5]u8 = [0; 5]
    var w := Out.New(mut m[..])
    WriteKeyUpdate(mut w, false)
    n := self.aead.Seal(TypeHandshake, m[..], mut self.inner[..], mut self.buf[..])
    try conn.WriteAll(io, self.buf[0..n])
    self.Rekey()
}

// WriteHalf.Owed sends the KeyUpdate the peer asked for, if one is owed,
// and moves the write key forward (L5).
WriteHalf.Owed(mut self, conn: &net.Conn, sh: &Mutex[Shared], io: Io): Error!void {
    var g := try sh.Lock(io.NoCancel())
    owe := g.Ref().owe
    g.Unlock()
    if !owe {
        return
    }
    var m: [5]u8 = [0; 5]
    var w := Out.New(mut m[..])
    WriteKeyUpdate(mut w, false)
    try self.Send(conn, io, TypeHandshake, m[..])
    self.Rekey()
    var g2 := try sh.Lock(io.NoCancel())
    g2.Mut().owe = false
    g2.Unlock()
}

// SendAlert writes one alert record of description code, at the fatal
// level unless it is close_notify or no_renegotiation (RFC 8446 6, RFC
// 5246 7.2.2).
SendAlert(w: mut WriteHalf, conn: &net.Conn, io: Io, code: u8): Error!void {
    level: u8 = if code == AlertCloseNotify || code == AlertNoRenegotiation { 1 } else { 2 }
    body: [2]u8 = [level, code]
    try w.Send(conn, io, TypeAlert, body[..])
}

// AlertFor is the alert a failure is reported to the peer with, 0 for one
// the peer caused by closing or that is the caller's (a cancel, a lost
// transport): protocol violations carry the alert their site chose, and a
// certificate the check refused the alert RFC 8446 6.2 names for it.
AlertFor(e: &Error, chosen: u8): u8 {
    return match *e {
        Protocol { if chosen == 0 { AlertDecodeError } else { chosen } }
        Unsupported { if chosen == 0 { AlertHandshakeFailure } else { chosen } }
        x509.UnknownAuthority { AlertUnknownCa }
        x509.Expired { AlertCertificateExpired }
        x509.Unsupported { AlertUnsupportedCertificate }
        x509.Malformed | x509.NameMismatch | x509.BadSignature | x509.BadUsage | x509.TooComplex { AlertBadCertificate }
        _ { 0 }
    }
}

/// Reads at most buf.Len() bytes of application data and returns their
/// count, 0 once the peer sent close_notify. Reads of this Conn run one at
/// a time. Opens at most one record per call into its own buffer;
/// allocates nothing.
///
/// Errors: the peer's Alert, Protocol (an alert was sent), and the
/// connection's net.Error, with Reset when it ends without close_notify.
/// After a failure every call reports it. A cancelled Read loses
/// nothing, so it can be called again.
export Conn.Read(&self, io: Io, buf: mut []u8): Error!int {
    hv := self.box.Ref()
    try hv.Check(io)
    if buf.Len() == 0 || hv.ReadShut(io) {
        return 0
    }
    var g := try hv.rd.Lock(io)
    var alert: u8 = 0
    n := g.Mut().Data(&self.conn, &hv.sh, io, buf, mut alert) else e {
        g.Unlock()
        match e {
            Canceled {
                fail e
            }
            _ {
                if hv.ReadShut(io) {
                    // Woken by Shutdown(Read) or (Both): the read direction
                    // ended, nothing failed (L6).
                    return 0
                }
                // A Read woken because the other half failed reports that
                // failure, not the shut-down transport's (L4).
                fail hv.Fail(io, &self.conn, e, AlertFor(&e, alert))
            }
        }
    }
    g.Unlock()
    return n
}

// Halves.ReadShut reports whether Shutdown ended the read direction; sh
// is held for one load, its lock taken with the cancellation masked.
Halves.ReadShut(&self, io: Io): bool {
    var g := self.sh.Lock(io.NoCancel()) else {
        return false
    }
    r := g.Ref().rshut
    g.Unlock()
    return r
}

/// Sends every byte of data, as records of at most 16 KiB, before any
/// other WriteAll of this Conn sends one. It first sends the KeyUpdate the
/// peer asked for, if one is owed. O(data); allocates nothing.
///
/// Errors: the connection's net.Error, and Closed once close_notify was
/// sent or the write side shut down, which leaves reading working (a
/// half-close). After a failure every call reports it. An interrupted
/// WriteAll, a cancelled one included, leaves the connection failed, and
/// every later call but Abandon fails, unless a shutdown ended it (Closed).
export Conn.WriteAll(&self, io: Io, data: []u8): Error!void {
    hv := self.box.Ref()
    try hv.Check(io)
    var g := hv.wr.Lock(io) else e {
        fail hv.Fail(io, &self.conn, e, 0)
    }
    // After close_notify or a shutdown the write direction is closed, not
    // failed: a half-close leaves Read working (RFC 8446 6.1), so Closed
    // is reported and nothing is stored.
    if !hv.Begin(io) {
        g.Unlock()
        fail Closed
    }
    WriteData(g.Mut(), &self.conn, &hv.sh, io, data) else e {
        shut := hv.End(io)
        g.Unlock()
        if shut {
            // Shutdown(Write) or (Both) ended this WriteAll (L6).
            fail Closed
        }
        fail hv.Fail(io, &self.conn, e, 0)
    }
    _ = hv.End(io)
    g.Unlock()
}

// Halves.Begin starts a WriteAll under wr: false when close_notify was
// sent or the write side was shut down, else it marks the WriteAll as
// sending, in the same sh section in which Shutdown reads the mark (L6).
// The lock is taken with the cancellation masked.
Halves.Begin(&self, io: Io): bool {
    var g := self.sh.Lock(io.NoCancel()) else {
        return false
    }
    s := g.Ref()
    if s.notified || s.wshut {
        g.Unlock()
        return false
    }
    g.Mut().writing = true
    g.Unlock()
    return true
}

// Halves.End ends a WriteAll's mark and reports whether Shutdown ended
// the write direction meanwhile.
Halves.End(&self, io: Io): bool {
    var g := self.sh.Lock(io.NoCancel()) else {
        return false
    }
    g.Mut().writing = false
    shut := g.Ref().wshut
    g.Unlock()
    return shut
}

// Halves.Mark records which directions a Shutdown ends (L6) and reports
// whether a WriteAll is sending at that point; one sh section, so a
// WriteAll either is marked here or finds wshut at its Begin.
Halves.Mark(&self, io: Io, read: bool, write: bool): bool {
    var g := self.sh.Lock(io.NoCancel()) else {
        return false
    }
    if read {
        g.Mut().rshut = true
    }
    if write {
        g.Mut().wshut = true
    }
    busy := g.Ref().writing
    g.Unlock()
    return busy
}

// Halves.Down records that the transport's write side is about to be
// shut: a later Notify has nothing to send (L6).
Halves.Down(&self, io: Io) {
    var g := self.sh.Lock(io.NoCancel()) else {
        return
    }
    g.Mut().down = true
    g.Unlock()
}

// Conn.Shutdown ends one or both directions (as net.Transport requires,
// L6): the read side by its mark and the transport's, so a Read waiting
// in another task returns 0; the write side by close_notify (RFC 8446
// 6.1) within the close limit and then the transport's, or, when a
// WriteAll is sending, by the transport's at once, which ends that
// WriteAll with Closed. Errors: Notify's, and the transport's.
Conn.Shutdown(&self, io: Io, how: net.Shutdown): Error!void {
    hv := self.box.Ref()
    read := match how {
        .Write { false }
        _ { true }
    }
    write := match how {
        .Read { false }
        _ { true }
    }
    busy := hv.Mark(io, read, write)
    if write {
        if !busy {
            try self.Notify(io)
        }
        hv.Down(io)
    }
    try self.conn.Shutdown(io, how)
}

// WriteData is WriteAll under wr, close_notify not sent: the owed
// KeyUpdate, then the data.
WriteData(w: mut WriteHalf, conn: &net.Conn, sh: &Mutex[Shared], io: Io, data: []u8): Error!void {
    try w.Owed(conn, sh, io)
    if data.Len() > 0 {
        try w.Send(conn, io, TypeAppData, data)
    }
}

// ConnFill and ConnSend are a Reader's fills and a Writer's writes: any
// error is ReadError or WriteError.
ConnFill(c: &Conn, io: Io, dst: mut []u8): ReadError!int {
    return c.Read(io, dst) else {
        fail ReadError
    }
}

ConnSend(c: &Conn, io: Io, b: []u8): WriteError!void {
    c.WriteAll(io, b) else {
        fail WriteError
    }
}

// A Conn has no flush of its own: WriteAll has sent every byte.
ConnFlush(c: &Conn, io: Io): WriteError!void {
}

const ConnReads = core.ReadPort[Conn].New(ConnFill)
const ConnWrites = core.WritePort[Conn].New(ConnSend, ConnFlush)

/// A buffered Reader for this Conn, with buf as its buffer. Each fill is
/// one Conn.Read, so two Readers of one Conn take turns per fill. Any
/// error is its ReadError; whether it was a timeout is read from the
/// scope. Bytes it buffered and the caller did not read are lost when it is
/// dropped. O(1); allocates nothing.
export Conn.Reader(&self, io: Io, buf: mut []u8): Reader from self, buf {
    return (&ConnReads).Reader(io, self, buf)
}

/// A buffered Writer for this Conn, with buf as its buffer. Each flush of
/// the buffer is one Conn.WriteAll. Any error is its WriteError. Flush it
/// before you drop it. O(1); allocates nothing.
export Conn.Writer(&self, io: Io, buf: mut []u8): Writer from self, buf {
    return (&ConnWrites).Writer(io, self, buf)
}

/// The application protocol (ALPN, RFC 7301) the handshake chose, or none
/// when none was offered or the peer chose none. O(1); allocates nothing.
export Conn.Alpn(&self): ?str from self {
    hv := self.box.Ref()
    if hv.alpn_len == 0 {
        return none
    }
    s := str.From(hv.alpn[0..hv.alpn_len]) else {
        return none
    }
    return some(s)
}

// Conn.Notify sends close_notify (RFC 8446 6.1), once, under wr, after an
// owed KeyUpdate, within the close limit: a scope opened from io, so a
// NoCancel io still gives up at the limit. Past it, time.TimedOut; an
// interruption leaves the connection failed (L4).
Conn.Notify(&self, io: Io): Error!void {
    hv := self.box.Ref()
    try hv.Check(io)
    scope s := io.Scope() {
        s.CancelAfter(hv.close_limit)
        NotifyOnce(hv, &self.conn, io) else e {
            canceled := match e {
                Canceled { true }
                _ { false }
            }
            err: Error = if task.LimitHit(&s, canceled) { time.TimedOut } else { e }
            fail hv.Fail(io, &self.conn, err, 0)
        }
    }
}

// NotifyOnce is Notify's work under wr.
NotifyOnce(hv: &Halves, conn: &net.Conn, io: Io): Error!void {
    var g := try hv.wr.Lock(io)
    NotifyLocked(g.Mut(), conn, &hv.sh, io) else e {
        g.Unlock()
        fail e
    }
    g.Unlock()
}

NotifyLocked(w: mut WriteHalf, conn: &net.Conn, sh: &Mutex[Shared], io: Io): Error!void {
    var g := try sh.Lock(io.NoCancel())
    // After a shutdown of the transport's write side nothing more can be
    // sent (L6).
    done := g.Ref().notified || g.Ref().down
    g.Unlock()
    if done {
        return
    }
    try w.Owed(conn, sh, io)
    try SendAlert(w, conn, io, AlertCloseNotify)
    var g2 := try sh.Lock(io.NoCancel())
    g2.Mut().notified = true
    g2.Unlock()
}

/// Sends close_notify to the peer, then releases the connection and its
/// state, every key wiped. Frees the state; allocates nothing.
///
/// Errors: Canceled under a cancelled scope; time.TimedOut past the
/// Config's close limit, also under a NoCancel Io and for a cancel that
/// comes after the limit passed; the connection's net.Error; an earlier
/// failure of the Conn. On any of them the Conn stays the caller's, failed,
/// to Abandon: write `onerr c.Abandon()` with `try c.Close(io)`.
export Conn.Close(self, io: Io): Error!void {
    try self.Notify(io)
    self.Abandon()
}

/// Releases the connection without speaking to the peer: every key wiped,
/// the state freed, the transport abandoned. Use it on error paths. Never
/// blocks or fails.
export Conn.Abandon(self) {
    Conn(conn, heap, box) := self
    FreeHalves(box, heap)
    conn.Abandon()
}
