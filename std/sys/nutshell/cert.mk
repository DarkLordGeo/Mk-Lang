// Why: std.sys for os "nutshell" is a placeholder that must export exactly
// what std/sys/linux exports, so std checks and builds for the target until
// the Nutshell backend replaces this directory.
//
// What: CertChainMax and CertVerify.
//
// How: CertVerify answers Unsupported.

/// CertChainMax has std/sys/linux's meaning.
export const CertChainMax: int = 16

/// CertVerify has std/sys/linux's contract; this placeholder answers
/// Unsupported. O(1); allocates nothing.
export CertVerify(chain: []u8, host: []u8, now: i64): OsResult {
    return NotHere()
}
