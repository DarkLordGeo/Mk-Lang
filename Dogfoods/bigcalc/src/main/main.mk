// Why: bigcalc is an arbitrary-precision RPN calculator with a scripting
// mode, plus tools for certificates, keys, signatures, encryption and
// digests.
//
// What: Main, the help text and Usage.
//
// How: the first argument picks a command module; each returns an exit code.

const Help: str = """
usage: bigcalc <command> [options] [arguments]

Calculator:
  calc [options] LINE...     evaluate each argument as a line of RPN, then
                             print the top of the stack
  run [options] SCRIPT|-     evaluate a script, one line at a time
    --base N                 output base, 2 to 36 (default 10)
    --max-digits N           longest number literal (default 100000)
    --max-bits N             largest result in bits (default 4000000)
    --mem-limit BYTES        keep the stack and variables in this many bytes
    --max-time MS            stop a script after MS milliseconds (run)
    --keep-going             report a failed line and go on (run)

Certificates and keys:
  cert show FILE...          print each certificate of PEM or DER files
  cert verify --roots FILE [--roots FILE...] [--host NAME] [--at TIME] CHAIN
                             verify a chain (leaf first) against the roots
  cert der FILE              print the DER structure of a file
  key show FILE              print a public or private key
  key public FILE            write the public key of a key as PEM
  sign --key FILE [--format der|fixed] [--out hex|base64] MESSAGE
                             sign with a P-256 or Ed25519 key
  verify --key FILE [--hash sha256|sha384|sha512] [--pss] [--format der|fixed]
         [--sig-encoding raw|hex|base64] MESSAGE SIGNATURE
                             check an RSA, ECDSA or Ed25519 signature

Encryption, tags and digests:
  enc seal|open --key-from SPEC [--salt SPEC] [--info TEXT] [--nonce HEX]
         [--aad TEXT] [--bits 128|256] [--armor] IN OUT
                             AES-GCM under a key derived with HKDF-SHA256
  mac --key SPEC [--hash sha256|sha384|sha512] [--expect HEX] FILE
  hash [--alg sha1|sha256|sha384|sha512|all] [--base64] FILE...
  encode base64|base64url|hex FILE
  decode base64|base64url|hex FILE

A SPEC is hex:..., b64:..., text:... or file:PATH.

Exit codes: 0 success, 1 error, 2 usage, 3 memory limit, 4 time limit,
5 a signature, tag or certificate that does not verify.
"""

/// Main runs the command its first argument names and returns its exit
/// code (see Help).
export Main(sys: &System): int {
    if sys.args.Len() < 2 {
        sys.PrintErr("{Help}", (Help)) else {}
        return ExitUsage
    }
    rest := sys.args[2..]
    return match sys.args[1] {
        "calc" { Calc(sys, rest) }
        "cert" { Cert(sys, rest) }
        "key" { KeyCmd(sys, rest) }
        "sign" { Sign(sys, rest) }
        "verify" { Verify(sys, rest) }
        "enc" { Enc(sys, rest) }
        "mac" { Mac(sys, rest) }
        "hash" { Hash(sys, rest) }
        "encode" { Encode(sys, rest) }
        "decode" { Decode(sys, rest) }
        "run" { Run(sys, rest) }
        "--help" | "-h" | "help" {
            sys.Print("{Help}", (Help)) else {}
            0
        }
        _ {
            cmd := sys.args[1]
            sys.PrintErr("bigcalc: unknown command {cmd}; see bigcalc --help\n", (cmd)) else {}
            ExitUsage
        }
    }
}

// Reports a bad command line and returns the usage exit code.
Usage(sys: &System, argv: []str, e: ArgError): int {
    match e {
        UnknownOption(arg) {
            name := argv[arg]
            sys.PrintErr("bigcalc: unknown option {name}\n", (name)) else {}
        }
        MissingValue(arg) {
            name := argv[arg]
            sys.PrintErr("bigcalc: {name} needs a value\n", (name)) else {}
        }
        OutOfMemory {
            sys.PrintErr("bigcalc: out of memory\n") else {}
        }
    }
    return ExitUsage
}

// Reports what stopped a command and returns its exit code.
Stop(sys: &System, what: str, code: int): int {
    sys.PrintErr("bigcalc: {what}\n", (what)) else {}
    return code
}
