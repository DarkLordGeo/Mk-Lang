// Why: TLS needs a certificate chain checked; std does not call macOS's
// Security framework, so std.x509 checks chains itself, as on Linux.
//
// What: CertChainMax and CertVerify.
//
// How: CertVerify answers Unsupported.

/// CertChainMax has std/sys/linux's meaning.
export const CertChainMax: int = 16

/// CertVerify has std/sys/linux's contract; on macOS it answers
/// Unsupported. O(1); allocates nothing.
export CertVerify(chain: []u8, host: []u8, now: i64): OsResult {
    return NotBuilt()
}
