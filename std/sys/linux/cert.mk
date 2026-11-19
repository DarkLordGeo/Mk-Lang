// Why: TLS needs a certificate chain checked, and Linux has no platform
// verifier to ask, unlike Windows.
//
// What: CertChainMax and CertVerify, with the same meaning as on Windows.
//
// How: CertVerify answers Unsupported, so std.x509 checks the chain itself
// against the system bundle (SSL_CERT_FILE, SSL_CERT_DIR and the usual
// paths). CertChainMax matches Windows' value.

import std.core

/// CertChainMax has std/sys/windows' meaning.
export const CertChainMax: int = 16

/// CertVerify has std/sys/windows' contract; Linux has no platform
/// verifier and answers Unsupported. O(1); allocates nothing.
export CertVerify(chain: []u8, host: []u8, now: i64): OsResult {
    return OsResult(value: 0, err: core.SysErr.Unsupported, os: 0)
}
