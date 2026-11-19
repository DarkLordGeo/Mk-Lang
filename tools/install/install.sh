#!/bin/sh
# Why: a Linux user needs one command that installs a released Mk toolchain,
# checked against its published hash, without root.
#
# What: installs the Mk toolchain on Linux. POSIX sh with curl or wget, and
# sha256sum or shasum.
#   curl -fsSL <release address>/latest/download/install.sh | sh
#   sh install.sh [--version 0.1.0] [--root <dir>] [--releases <address>] [--no-modify-path]
#
# How: it downloads the newest release (or --version) with its SHA256SUMS over
# https, checks the archive's SHA-256, unpacks it to
# <root>/toolchains/<version>, points the symbolic link <root>/current at
# it, and adds `export PATH=...` to ~/.profile unless --no-modify-path; it
# prints the line either way. root defaults to ~/.local/share/mk. A first
# install trusts https and the release host; every later `mk upgrade`
# checks the release's Ed25519 signature. The options also come from
# MK_VERSION, MK_HOME, MK_RELEASES and MK_NO_MODIFY_PATH=1.

set -eu

version="${MK_VERSION:-}"
root="${MK_HOME:-}"
releases="${MK_RELEASES:-https://github.com/zerodayokay/Mk-Lang/releases}"
modify_path=1
[ "${MK_NO_MODIFY_PATH:-}" = 1 ] && modify_path=0
host=x86_64-linux

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

case "$releases" in
https://*) ;;
http://127.0.0.1:* | http://127.0.0.1/* | http://localhost:* | http://localhost/* | http://\[::1\]:*) ;;
*) die "the release address $releases is not https" ;;
esac

file_url() { # version file
	if [ -n "$1" ]; then
		echo "${releases%/}/download/v$1/$2"
	else
		echo "${releases%/}/latest/download/$2"
	fi
}

loopback() { # url
	case "$1" in
	http://127.0.0.1:* | http://127.0.0.1/* | http://localhost:* | http://localhost/* | http://\[::1\]:* | http://\[::1\]/*) return 0 ;;
	esac
	return 1
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

fetch "$(file_url "$version" SHA256SUMS)" "$work/SHA256SUMS"

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
