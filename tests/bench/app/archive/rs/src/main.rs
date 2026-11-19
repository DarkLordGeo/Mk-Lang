// archive, Rust, eco sha2, flate2, ring and walkdir (Cargo.lock pins them):
// the task of ../../README.md. `create` walks the tree with walkdir, keeps
// the regular files, sorts them by their relative path with `/` in byte
// order, reads each whole with std::fs::read, hashes it with
// sha2::Sha256::digest and appends it to one payload Vec; the payload is
// compressed by flate2's DeflateEncoder at Compression::default() (level 6,
// miniz_oxide), the manifest is signed by ring's Ed25519KeyPair (from the
// fixed seed) and the archive body is sealed in place by ring's AES_256_GCM
// LessSafeKey under the fixed key and nonce. `open` reverses it: it opens
// the seal, verifies the signature with ring's UnparsedPublicKey, inflates
// the payload with flate2's DeflateDecoder, checks every file's SHA-256
// against the manifest, and prints the manifest and a summary line through
// a BufWriter over stdout.
// Usage: archive create <dir> <out> [<files>] | archive open <archive>.
use flate2::Compression;
use flate2::read::DeflateDecoder;
use flate2::write::DeflateEncoder;
use ring::aead::{AES_256_GCM, Aad, LessSafeKey, Nonce, UnboundKey};
use ring::signature::{ED25519, Ed25519KeyPair, KeyPair, UnparsedPublicKey};
use sha2::{Digest, Sha256};
use std::error::Error;
use std::fmt::Write as _;
use std::fs::File;
use std::io::{BufWriter, Read, Write};
use std::path::PathBuf;
use walkdir::WalkDir;

const MAGIC: &[u8; 8] = b"MKARCH01";
const HEX: &[u8; 16] = b"0123456789abcdef";

type Res<T> = Result<T, Box<dyn Error>>;

// The row's fixed test material (README.md, Format): the AES-256 key is
// the bytes 0 to 31, the nonce 160 to 171 and the Ed25519 seed 64 to 95.
fn fixed<const N: usize>(first: u8) -> [u8; N] {
    let mut b = [0u8; N];
    for (i, x) in b.iter_mut().enumerate() {
        *x = first + i as u8;
    }
    b
}

fn sealing_key() -> Res<LessSafeKey> {
    let key: [u8; 32] = fixed(0);
    let k = UnboundKey::new(&AES_256_GCM, &key).map_err(|_| "aes key")?;
    Ok(LessSafeKey::new(k))
}

fn nonce() -> Nonce {
    Nonce::assume_unique_for_key(fixed(160))
}

fn signer() -> Res<Ed25519KeyPair> {
    let seed: [u8; 32] = fixed(64);
    Ok(Ed25519KeyPair::from_seed_unchecked(&seed).map_err(|_| "ed25519 seed")?)
}

fn push_hex(s: &mut String, b: &[u8]) {
    for &x in b {
        s.push(HEX[(x >> 4) as usize] as char);
        s.push(HEX[(x & 15) as usize] as char);
    }
}

fn create(dir: &str, out: &str, limit: Option<usize>) -> Res<()> {
    let root = PathBuf::from(dir);
    let mut files: Vec<(String, PathBuf)> = Vec::new();
    for e in WalkDir::new(&root) {
        let e = e?;
        if !e.file_type().is_file() {
            continue;
        }
        let rel = e
            .path()
            .strip_prefix(&root)?
            .to_str()
            .ok_or("a name that is not UTF-8")?;
        files.push((rel.replace('\\', "/"), e.into_path()));
    }
    files.sort_by(|a, b| a.0.as_bytes().cmp(b.0.as_bytes()));
    if let Some(n) = limit {
        files.truncate(n);
    }
    let mut manifest = String::new();
    let mut payload: Vec<u8> = Vec::new();
    for (rel, path) in &files {
        let data = std::fs::read(path)?;
        push_hex(&mut manifest, &Sha256::digest(&data));
        writeln!(manifest, " {} {}", data.len(), rel)?;
        payload.extend_from_slice(&data);
    }
    let mut enc = DeflateEncoder::new(Vec::new(), Compression::default());
    enc.write_all(&payload)?;
    let packed = enc.finish()?;
    let sig = signer()?.sign(manifest.as_bytes());
    let mut body = Vec::with_capacity(8 + manifest.len() + 64 + packed.len() + 16);
    body.extend_from_slice(&(manifest.len() as u64).to_le_bytes());
    body.extend_from_slice(manifest.as_bytes());
    body.extend_from_slice(sig.as_ref());
    body.extend_from_slice(&packed);
    sealing_key()?
        .seal_in_place_append_tag(nonce(), Aad::from(MAGIC), &mut body)
        .map_err(|_| "seal")?;
    let mut f = File::create(out)?;
    f.write_all(MAGIC)?;
    f.write_all(&body)?;
    println!("archive: {} files, {} bytes", files.len(), payload.len());
    Ok(())
}

fn unhex(s: &[u8]) -> Res<[u8; 32]> {
    let digit = |c: u8| -> Res<u8> {
        match c {
            b'0'..=b'9' => Ok(c - b'0'),
            b'a'..=b'f' => Ok(c - b'a' + 10),
            _ => Err("a digest that is not lowercase hex".into()),
        }
    };
    let mut d = [0u8; 32];
    if s.len() != 64 {
        return Err("a digest that is not 64 hex digits".into());
    }
    for (i, x) in d.iter_mut().enumerate() {
        *x = digit(s[2 * i])? << 4 | digit(s[2 * i + 1])?;
    }
    Ok(d)
}

fn open(path: &str) -> Res<()> {
    let mut file = std::fs::read(path)?;
    if file.len() < 8 + 16 || &file[..8] != MAGIC {
        return Err("not an archive".into());
    }
    let mut sealed = file.split_off(8);
    let plain = sealing_key()?
        .open_in_place(nonce(), Aad::from(MAGIC), &mut sealed)
        .map_err(|_| "the seal does not open")?;
    if plain.len() < 8 + 64 {
        return Err("a cut archive".into());
    }
    let mlen = u64::from_le_bytes(plain[..8].try_into()?);
    if mlen > (plain.len() - 8 - 64) as u64 {
        return Err("a cut archive".into());
    }
    let mlen = mlen as usize;
    let manifest = &plain[8..8 + mlen];
    let sig = &plain[8 + mlen..8 + mlen + 64];
    let packed = &plain[8 + mlen + 64..];
    let kp = signer()?;
    UnparsedPublicKey::new(&ED25519, kp.public_key().as_ref())
        .verify(manifest, sig)
        .map_err(|_| "a bad signature")?;
    let mut entries: Vec<([u8; 32], usize)> = Vec::new();
    let mut total = 0usize;
    for line in manifest.split_inclusive(|&b| b == b'\n') {
        if line.len() < 68 || line[64] != b' ' || line[line.len() - 1] != b'\n' {
            return Err("a bad manifest line".into());
        }
        let rest = &line[65..line.len() - 1];
        let sp = rest
            .iter()
            .position(|&b| b == b' ')
            .ok_or("a bad manifest line")?;
        let size: usize = std::str::from_utf8(&rest[..sp])?.parse()?;
        entries.push((unhex(&line[..64])?, size));
        total = total.checked_add(size).ok_or("a bad manifest line")?;
    }
    let mut data = Vec::new();
    data.try_reserve_exact(total)
        .map_err(|_| "the manifest's sizes are too large")?;
    DeflateDecoder::new(packed)
        .take(total as u64 + 1)
        .read_to_end(&mut data)?;
    if data.len() != total {
        return Err("the payload is not the manifest's size".into());
    }
    let mut at = 0;
    for (digest, size) in &entries {
        if Sha256::digest(&data[at..at + size]).as_slice() != digest {
            return Err("a file's digest differs from the manifest".into());
        }
        at += size;
    }
    let mut w = BufWriter::with_capacity(64 << 10, std::io::stdout().lock());
    w.write_all(manifest)?;
    writeln!(w, "ok: {} files, {} bytes", entries.len(), total)?;
    w.flush()?;
    Ok(())
}

fn main() -> Res<()> {
    let args: Vec<String> = std::env::args().collect();
    match args.get(1).map(String::as_str) {
        Some("create") if args.len() == 4 || args.len() == 5 => {
            let limit = match args.get(4) {
                Some(n) => Some(n.parse()?),
                None => None,
            };
            create(&args[2], &args[3], limit)
        }
        Some("open") if args.len() == 3 => open(&args[2]),
        _ => Err("usage: archive create <dir> <out> [<files>] | archive open <archive>".into()),
    }
}
