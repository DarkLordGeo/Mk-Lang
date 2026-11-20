#!/bin/sh
# Why: a Linux or macOS user needs one command that installs a released Mk
# toolchain, signed by the project, without root, Go or LLVM.
#
# What: installs the Mk toolchain on x86_64 Linux and on Apple silicon
# Macs. On an Intel Mac it installs nothing and says how to build mk from
# source, since releases carry no Intel Mac archive yet. POSIX sh with curl or wget, sha256sum or shasum, and
# OpenSSL 3 or later or Perl (for the signature): what macOS ships is
# enough.
#   curl -fsSL https://github.com/zerodayokay/Mk-Lang/releases/latest/download/install.sh | sh
#   sh install.sh [--version 0.1.0] [--root <dir>] [--releases <address>] [--no-modify-path]
#
# How: it picks the host's archive from uname (a shell under Rosetta on
# Apple silicon still gets the arm64 one) and downloads the newest release
# (or --version): SHA256SUMS and SHA256SUMS.sig over https. It checks the
# Ed25519 signature against the release keys written below (the keys mk
# itself trusts, internal/release/keys.go) before it reads the list, with
# openssl, or with the Perl program below where openssl cannot check
# Ed25519 (macOS's LibreSSL), then the archive's SHA-256 against the list.
# It unpacks the archive to <root>/toolchains/<version>, points the
# symbolic link <root>/current at it, and adds `export PATH=...` to
# ~/.profile (on macOS also to ~/.zprofile, which zsh reads, and to
# ~/.bash_profile when it exists) unless --no-modify-path; it prints the
# line either way. root defaults to ~/.local/share/mk. Every later `mk
# upgrade` checks signatures the same way. The options also come from
# MK_VERSION, MK_HOME, MK_RELEASES and MK_NO_MODIFY_PATH=1.
# MK_INSTALL_TEST_KEY ("<key id> <base64 public key>") adds a trusted key
# and MK_INSTALL_HOST names the host, each only with a loopback release
# address (tests).
#
# curl sets no quarantine attribute on what it downloads, so macOS's
# Gatekeeper does not stop the unpacked mk, clang or lld from running.
#
# The signature protects against a changed archive or list. It cannot
# protect against a changed copy of this script: read the script first, or
# take it from the repository at a release tag.

set -eu

# The whole script is one function, called on the last line, so a download
# cut off part-way runs nothing: sh reads main to its closing brace before
# it runs any of it. Its body is not indented.
main() {

version="${MK_VERSION:-}"
root="${MK_HOME:-}"
releases="${MK_RELEASES:-https://github.com/zerodayokay/Mk-Lang/releases}"
modify_path=1
[ "${MK_NO_MODIFY_PATH:-}" = 1 ] && modify_path=0

# The release keys this script trusts: one "<key id> <base64 public key>"
# per line, the same keys as internal/release/keys.go (TestInstallKeys
# checks they agree). With none, nothing can be installed.
trusted_keys='
7f12cb260ba04bea ykIH33Kzl3Uno3l9vL2v5oOUy/H0toVHc5giQVn4BC8=
'

die() {
	echo "install.sh: $*" >&2
	exit 1
}

usage() {
	echo "usage: install.sh [--version <v>] [--root <dir>] [--releases <address>] [--no-modify-path]"
}

while [ $# -gt 0 ]; do
	case "$1" in
	--version) [ $# -ge 2 ] || die "--version needs a value"; version=$2; shift 2 ;;
	--version=*) version=${1#*=}; shift ;;
	--root) [ $# -ge 2 ] || die "--root needs a value"; root=$2; shift 2 ;;
	--root=*) root=${1#*=}; shift ;;
	--releases) [ $# -ge 2 ] || die "--releases needs a value"; releases=$2; shift 2 ;;
	--releases=*) releases=${1#*=}; shift ;;
	--no-modify-path) modify_path=0; shift ;;
	-h | --help) usage; exit 0 ;;
	*) usage >&2; die "unknown option $1" ;;
	esac
done

[ -n "$root" ] || root="$HOME/.local/share/mk"
# A relative root is the working directory's, made absolute now.
case "$root" in
/* | [A-Za-z]:[\\/]*) ;; # [A-Za-z]: is a Windows drive path under Git's sh (a developer check)
*) root="$(pwd)/$root" ;;
esac
version=${version#v}
if [ -n "$version" ] && ! printf '%s\n' "$version" | grep -Eqx '[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?'; then
	die "--version $version is not a version (major.minor.patch[-pre])"
fi
# The profile line holds root inside double quotes: refuse a root that
# would change its meaning there, before anything is written.
if [ "$modify_path" = 1 ]; then
	case "$root" in
	*'"'* | *'$'* | *'`'*) die "the root $root holds a quote, $ or a backquote, which a profile line cannot hold; pass --no-modify-path and set PATH by hand" ;;
	esac
fi

# loopback: whether url is plain http to 127.0.0.1, localhost or [::1],
# with an optional port. Only the authority counts, and one with userinfo
# (http://127.0.0.1:x@example.com/) is refused, since curl and wget connect
# to the host after the @.
loopback() { # url
	case "$1" in
	http://*) ;;
	*) return 1 ;;
	esac
	a=${1#http://}
	a=${a%%/*}
	a=${a%%\?*}
	a=${a%%#*}
	case "$a" in
	*@*) return 1 ;;
	esac
	case "$a" in
	127.0.0.1 | localhost | \[::1\]) return 0 ;;
	127.0.0.1:* | localhost:* | \[::1\]:*) ;;
	*) return 1 ;;
	esac
	port=${a##*:}
	case "$port" in
	'' | *[!0-9]*) return 1 ;;
	esac
	return 0
}

case "$releases" in
https://*) ;;
*) loopback "$releases" || die "the release address $releases is not https" ;;
esac

# The host's archive: x86_64 Linux or Apple silicon macOS. x86_64-macos is
# named here so an Intel Mac gets its own refusal below.
os=$(uname -s)
arch=$(uname -m)
host=
case "$os" in
Linux)
	case "$arch" in
	x86_64 | amd64) host=x86_64-linux ;;
	esac
	;;
Darwin)
	case "$arch" in
	arm64) host=aarch64-macos ;;
	x86_64)
		host=x86_64-macos
		# A shell under Rosetta reports x86_64 on Apple silicon, where the
		# arm64 toolchain runs natively.
		if [ "$(sysctl -n hw.optional.arm64 2>/dev/null)" = 1 ]; then
			host=aarch64-macos
		fi
		;;
	esac
	;;
esac
# A host for tests, taken only with a loopback release address.
if [ -n "${MK_INSTALL_HOST:-}" ] && loopback "$releases"; then
	case "$MK_INSTALL_HOST" in
	x86_64-linux | x86_64-macos | aarch64-macos) host=$MK_INSTALL_HOST ;;
	*) die "MK_INSTALL_HOST=$MK_INSTALL_HOST is not a host install.sh installs" ;;
	esac
fi
if [ "$host" = x86_64-macos ]; then
	die "there is no binary release for Intel Macs yet; build mk from source with Homebrew's LLVM 21 (brew install llvm@21 lld@21), as README.md's \"Building from source\" says. Nothing was installed"
fi
if [ -z "$host" ]; then
	case "$os" in
	MINGW* | MSYS* | CYGWIN*) die "on Windows, install with install.ps1" ;;
	esac
	die "releases carry no archive for $os $arch (only x86_64 Linux, x86_64 Windows and Apple silicon macOS); build mk from source"
fi

file_url() { # version file
	if [ -n "$1" ]; then
		echo "${releases%/}/download/v$1/$2"
	else
		echo "${releases%/}/latest/download/$2"
	fi
}


# fetch follows at most ten redirects itself, each target checked: https,
# or plain http only to a loopback host (tests), so a redirect cannot
# downgrade the download.
fetch() { # url file
	url=$1
	n=0
	while :; do
		case "$url" in
		https://*) ;;
		*) loopback "$url" || die "redirected to $url, which is not https; nothing was installed" ;;
		esac
		if command -v curl >/dev/null 2>&1; then
			loc=$(curl -fsS --proto '=https,http' -o "$2" -w '%{redirect_url}' "$url") || die "downloading $url failed"
		elif command -v wget >/dev/null 2>&1; then
			if hdr=$(wget -q -S --max-redirect=0 -O "$2" "$url" 2>&1); then
				loc=
			else
				loc=$(printf '%s\n' "$hdr" | sed -n 's/^ *[Ll]ocation: *//p' | tail -n 1 | tr -d '\r')
				[ -n "$loc" ] || die "downloading $url failed"
			fi
		else
			die "neither curl nor wget is installed"
		fi
		[ -n "$loc" ] || return 0
		case "$loc" in
		*://*) url=$loc ;;
		/*) url="$(printf '%s' "$url" | sed -E 's#^([A-Za-z]+://[^/]+).*#\1#')$loc" ;;
		*) die "downloading $url: a relative redirect to $loc" ;;
		esac
		n=$((n + 1))
		[ "$n" -le 10 ] || die "more than 10 redirects"
	done
}

sha256() { # file
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum <"$1" | cut -d" " -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 <"$1" | cut -d" " -f1
	else
		die "neither sha256sum nor shasum is installed"
	fi
}

toolchains="$root/toolchains"
mkdir -p "$toolchains"
work=$(mktemp -d "$toolchains/.install-XXXXXX")
trap 'rm -rf "$work"' EXIT INT TERM

# ed25519_pl checks an Ed25519 signature as Go's crypto/ed25519 does (S
# below the group order, [S]B - [k]A encoded equal to R), for a shell whose
# openssl cannot (macOS ships LibreSSL). It needs only Perl's core modules,
# which macOS's own Perl has. Arguments: the base64 public key, the base64
# signature, the signed file; it exits 0 for a good signature. The program
# holds no single quote.
ed25519_pl='
use strict;
use warnings;
use Math::BigInt try => q(GMP);
use Digest::SHA qw(sha512);
use MIME::Base64 qw(decode_base64);
my ($pub, $sig, $file) = (decode_base64($ARGV[0]), decode_base64($ARGV[1]), $ARGV[2]);
exit 1 unless length($pub) == 32 && length($sig) == 64;
open(my $fh, q(<), $file) or exit 1;
binmode($fh);
my $msg = do { local $/; <$fh> };
close($fh);
my $p = Math::BigInt->new(2)->bpow(255)->bsub(19);
my $l = Math::BigInt->new(2)->bpow(252)->badd(Math::BigInt->new(q(27742317777372353535851937790883648493)));
my $d = Math::BigInt->new(q(37095705934669439343138083508754565189542113879843219016388785533085940283555));
my $d2 = ($d * 2) % $p;
my $i = Math::BigInt->new(q(19681161376707505956807079304988542015446066515923890162744021073123829784752));
# Little-endian bytes to a number and back; the inverse mod p.
sub num { Math::BigInt->from_hex(unpack(q(H*), scalar reverse($_[0]))) }
sub enc { my $h = $_[0]->as_hex; $h =~ s/^0x//; scalar reverse(pack(q(H*), (q(0) x (64 - length($h))) . $h)) }
sub inv { $_[0]->copy->bmodpow($p - 2, $p) }
# Point addition in extended coordinates (RFC 8032, 5.1.4), also doubling.
sub add {
    my ($x1, $y1, $z1, $t1, $x2, $y2, $z2, $t2) = (@{$_[0]}, @{$_[1]});
    my $a = (($y1 - $x1) * ($y2 - $x2)) % $p;
    my $b = (($y1 + $x1) * ($y2 + $x2)) % $p;
    my $c = ($t1 * $d2 * $t2) % $p;
    my $e = ($z1 * 2 * $z2) % $p;
    my ($ee, $f, $g, $h) = ($b - $a, $e - $c, $e + $c, $b + $a);
    return [($ee * $f) % $p, ($g * $h) % $p, ($f * $g) % $p, ($ee * $h) % $p];
}
# -A from the public key: y and the sign of x (RFC 8032, 5.1.3).
my $yb = $pub;
my $sign = ord(substr($yb, 31, 1)) >> 7;
substr($yb, 31, 1) = chr(ord(substr($yb, 31, 1)) & 127);
my $y = num($yb) % $p;
my $y2 = ($y * $y) % $p;
my $x2 = (($y2 - 1) * inv(($d * $y2 + 1) % $p)) % $p;
my $x = $x2->copy->bmodpow(($p + 3) / 8, $p);
$x = ($x * $i) % $p if (($x * $x - $x2) % $p) != 0;
exit 1 if (($x * $x - $x2) % $p) != 0;
$x = ($p - $x) % $p if ($x->is_odd ? 1 : 0) != $sign;
my $na = [($p - $x) % $p, $y, Math::BigInt->new(1), (($p - $x) * $y) % $p];
my $bx = Math::BigInt->new(q(15112221349535400772501151409588531511454012693041857206046113283949847762202));
my $by = Math::BigInt->new(q(46316835694926478169428394003475163141307993866256225615783033603165251855960));
my $bb = [$bx, $by, Math::BigInt->new(1), ($bx * $by) % $p];
my $s = num(substr($sig, 32, 32));
exit 1 if $s >= $l;
my $k = num(sha512(substr($sig, 0, 32) . $pub . $msg)) % $l;
# [S]B + [k](-A), both scalars in one pass of doublings.
my $both = add($bb, $na);
my $r = [Math::BigInt->new(0), Math::BigInt->new(1), Math::BigInt->new(1), Math::BigInt->new(0)];
for (my $n = 255; $n >= 0; $n--) {
    $r = add($r, $r);
    my ($sb, $kb) = ($s->copy->brsft($n)->is_odd, $k->copy->brsft($n)->is_odd);
    $r = add($r, $sb && $kb ? $both : $sb ? $bb : $na) if $sb || $kb;
}
my $zi = inv($r->[2]);
my ($rx, $ry) = (($r->[0] * $zi) % $p, ($r->[1] * $zi) % $p);
$ry->badd(Math::BigInt->new(2)->bpow(255)) if $rx->is_odd;
exit(enc($ry) eq substr($sig, 0, 32) ? 0 : 1);
'

# verify_sums checks SHA256SUMS.sig, one line "<key id> <base64 Ed25519
# signature>", over SHA256SUMS against trusted_keys: with openssl, and
# with ed25519_pl when openssl is missing or refuses (it cannot tell a bad
# signature from an algorithm it lacks). Only a verifier's yes installs.
verify_sums() { # sums sig
	keys=$trusted_keys
	# A key for tests, taken only with a loopback release address.
	if [ -n "${MK_INSTALL_TEST_KEY:-}" ] && loopback "$releases"; then
		keys="$keys
$MK_INSTALL_TEST_KEY"
	fi
	printf '%s\n' "$keys" | grep -Eq '^[0-9a-f]{16} [A-Za-z0-9+/]{43}=$' ||
		die "this script trusts no release key yet, so it cannot check a release; nothing was installed"
	[ "$(grep -c '' "$2")" = 1 ] && [ "$(wc -l <"$2" | tr -d ' ')" = 1 ] ||
		die "SHA256SUMS.sig is not one line; nothing was installed"
	sig=$(cat "$2")
	printf '%s\n' "$sig" | grep -Eqx '[0-9a-f]{16} [A-Za-z0-9+/]{86}==' ||
		die "SHA256SUMS.sig is not \"<key id> <signature>\"; nothing was installed"
	id=${sig%% *}
	key=$(printf '%s\n' "$keys" | awk -v id="$id" '$1 == id { print $2; exit }')
	[ -n "$key" ] || die "the release is signed by key $id, which this script does not trust; nothing was installed"
	sigb64=${sig#* }
	# An Ed25519 public key in PEM: the 12-byte DER prefix is 16 base64
	# digits, so the key's own base64 follows it unchanged.
	printf -- '-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEA%s\n-----END PUBLIC KEY-----\n' "$key" >"$work/key.pem"
	if command -v openssl >/dev/null 2>&1 &&
		printf '%s' "$sigb64" | openssl base64 -d -A >"$work/sig.bin" 2>/dev/null &&
		openssl pkeyutl -verify -pubin -inkey "$work/key.pem" -rawin -in "$1" -sigfile "$work/sig.bin" >/dev/null 2>&1; then
		echo "SHA256SUMS is signed by release key $id"
		return 0
	fi
	if command -v perl >/dev/null 2>&1 && perl -MMath::BigInt -MDigest::SHA -MMIME::Base64 -e 1 >/dev/null 2>&1; then
		perl -e "$ed25519_pl" "$key" "$sigb64" "$1" ||
			die "the signature does not match SHA256SUMS; nothing was installed"
		echo "SHA256SUMS is signed by release key $id (checked with Perl)"
		return 0
	fi
	die "the signature does not match SHA256SUMS, or nothing here can check Ed25519 (it needs OpenSSL 3 or later, or Perl); nothing was installed"
}

fetch "$(file_url "$version" SHA256SUMS)" "$work/SHA256SUMS"
fetch "$(file_url "$version" SHA256SUMS.sig)" "$work/SHA256SUMS.sig"
verify_sums "$work/SHA256SUMS" "$work/SHA256SUMS.sig"

# The host's archives in SHA256SUMS: "<hash> <name> <version>" per line.
tok='[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?'
grep -E "^[0-9a-f]{64}  mk-$tok-$host\.tar\.gz\$" "$work/SHA256SUMS" |
	sed -E "s/^([0-9a-f]{64})  (mk-($tok)-$host\.tar\.gz)\$/\1 \2 \3/" >"$work/list" || true
if [ -n "$version" ]; then
	line=$(awk -v v="$version" '$3 == v { print; exit }' "$work/list")
	[ -n "$line" ] || die "mk $version has no $host archive in its SHA256SUMS"
else
	# The newest: sort by the numbers of major.minor.patch, a release after
	# its pre-releases (a pre-release sorts before the release by a 0 key).
	line=$(awk '{ split($3, a, "-"); split(a[1], n, "."); pre = (a[2] == "") ? 1 : 0;
		printf "%d %d %d %d %s %s\n", n[1], n[2], n[3], pre, (a[2] == "") ? "~" : a[2], $0 }' "$work/list" |
		sort -k1,1n -k2,2n -k3,3n -k4,4n -k5,5 | tail -n 1 | cut -d' ' -f6-)
	[ -n "$line" ] || die "the release has no $host archive in its SHA256SUMS"
fi
want=$(echo "$line" | cut -d' ' -f1)
name=$(echo "$line" | cut -d' ' -f2)
ver=$(echo "$line" | cut -d' ' -f3)
dest="$toolchains/$ver"

if [ -e "$dest" ]; then
	echo "mk $ver is already in $dest"
else
	fetch "$(file_url "$ver" "$name")" "$work/$name"
	got=$(sha256 "$work/$name")
	[ "$got" = "$want" ] || die "$name: SHA-256 $got, but SHA256SUMS says $want; nothing was installed"
	mkdir "$work/tree"
	(cd "$work/tree" && gzip -dc | tar -xf -) <"$work/$name"
	top="$work/tree/mk-$ver-$host"
	[ -f "$top/bin/mk" ] || die "$name holds no bin/mk; nothing was installed"
	mv "$top" "$dest"
	echo "unpacked mk $ver to $dest"
fi

# current: a new link renamed over the old one where mv -T exists (GNU),
# else ln -sfn.
if mv --help 2>/dev/null | grep -q -- '-T'; then
	ln -s "$dest" "$root/.current-new.$$"
	mv -T "$root/.current-new.$$" "$root/current"
else
	ln -sfn "$dest" "$root/current"
fi
echo "current is mk $ver"

# Inside the line's double quotes a backslash escapes the next one, so
# each is doubled (a quote, $ and a backquote were refused above).
rootq=$(printf '%s\n' "$root" | sed 's/\\/\\\\/g')
line="export PATH=\"$rootq/current/bin:\$PATH\""
add_line() { # profile
	if [ -f "$1" ] && grep -qxF "$line" "$1"; then
		echo "$1 already has: $line"
	else
		printf '\n# Mk (install.sh)\n%s\n' "$line" >>"$1"
		echo "added to $1: $line"
	fi
}
if [ "$modify_path" = 1 ]; then
	add_line "$HOME/.profile"
	# macOS's login shell is zsh, which reads ~/.zprofile and not
	# ~/.profile; bash reads ~/.bash_profile in place of ~/.profile when it
	# exists.
	if [ "$os" = Darwin ]; then
		add_line "$HOME/.zprofile"
		if [ -f "$HOME/.bash_profile" ]; then
			add_line "$HOME/.bash_profile"
		fi
	fi
else
	echo "no shell profile was changed; add this line to your shell profile: $line"
fi

}

main "$@"
