#!/bin/sh
# Why: a Linux user needs one command that installs a released Mk toolchain,
# signed by the project, without root, Go or LLVM.
#
# What: installs the Mk toolchain on Linux. POSIX sh with curl or wget,
# sha256sum or shasum, and OpenSSL 3 or later (for the signature).
#   curl -fsSL https://github.com/zerodayokay/Mk-Lang/releases/latest/download/install.sh | sh
#   sh install.sh [--version 0.1.0] [--root <dir>] [--releases <address>] [--no-modify-path]
#
# How: it downloads the newest release (or --version): SHA256SUMS and
# SHA256SUMS.sig over https. It checks the Ed25519 signature against the
# release keys written below (the keys mk itself trusts,
# internal/release/keys.go) before it reads the list, then the archive's
# SHA-256 against the list. It unpacks the archive to
# <root>/toolchains/<version>, points the symbolic link <root>/current at
# it, and adds `export PATH=...` to ~/.profile unless --no-modify-path; it
# prints the line either way. root defaults to ~/.local/share/mk. Every
# later `mk upgrade` checks signatures the same way. The options also come
# from MK_VERSION, MK_HOME, MK_RELEASES and MK_NO_MODIFY_PATH=1.
# MK_INSTALL_TEST_KEY ("<key id> <base64 public key>") adds a trusted key,
# and only with a loopback release address (tests).
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
host=x86_64-linux

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

# verify_sums checks SHA256SUMS.sig, one line "<key id> <base64 Ed25519
# signature>", over SHA256SUMS against trusted_keys, with openssl.
verify_sums() { # sums sig
	keys=$trusted_keys
	# A key for tests, taken only with a loopback release address.
	if [ -n "${MK_INSTALL_TEST_KEY:-}" ] && loopback "$releases"; then
		keys="$keys
$MK_INSTALL_TEST_KEY"
	fi
	printf '%s\n' "$keys" | grep -Eq '^[0-9a-f]{16} [A-Za-z0-9+/]{43}=$' ||
		die "this script trusts no release key yet, so it cannot check a release; nothing was installed"
	command -v openssl >/dev/null 2>&1 || die "openssl is not installed; it checks the release's signature (OpenSSL 3 or later)"
	[ "$(grep -c '' "$2")" = 1 ] && [ "$(wc -l <"$2" | tr -d ' ')" = 1 ] ||
		die "SHA256SUMS.sig is not one line; nothing was installed"
	sig=$(cat "$2")
	printf '%s\n' "$sig" | grep -Eqx '[0-9a-f]{16} [A-Za-z0-9+/]{86}==' ||
		die "SHA256SUMS.sig is not \"<key id> <signature>\"; nothing was installed"
	id=${sig%% *}
	key=$(printf '%s\n' "$keys" | awk -v id="$id" '$1 == id { print $2; exit }')
	[ -n "$key" ] || die "the release is signed by key $id, which this script does not trust; nothing was installed"
	# An Ed25519 public key in PEM: the 12-byte DER prefix is 16 base64
	# digits, so the key's own base64 follows it unchanged.
	printf -- '-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEA%s\n-----END PUBLIC KEY-----\n' "$key" >"$work/key.pem"
	printf '%s' "${sig#* }" | openssl base64 -d -A >"$work/sig.bin" 2>/dev/null ||
		die "SHA256SUMS.sig holds no base64 signature; nothing was installed"
	openssl pkeyutl -verify -pubin -inkey "$work/key.pem" -rawin -in "$1" -sigfile "$work/sig.bin" >/dev/null 2>&1 ||
		die "the signature does not match SHA256SUMS (or this openssl cannot check Ed25519: OpenSSL 3 or later is needed); nothing was installed"
	echo "SHA256SUMS is signed by release key $id"
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
if [ "$modify_path" = 1 ]; then
	profile="$HOME/.profile"
	if [ -f "$profile" ] && grep -qxF "$line" "$profile"; then
		echo "$profile already has: $line"
	else
		printf '\n# Mk (install.sh)\n%s\n' "$line" >>"$profile"
		echo "added to $profile: $line"
	fi
else
	echo "~/.profile was not changed; add this line to your shell profile: $line"
fi

}

main "$@"
