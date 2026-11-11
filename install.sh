#!/bin/sh
# qemu-tui installer for Linux, FreeBSD, macOS and other Unix-like systems.
#
# Builds qemu-tui from this source tree and installs the binary and the man page.
# Run it from a checkout / unpacked archive:   sh install.sh [options]
# See --help for the options.

set -eu

APP=qemu-tui
MIN_GO_MAJOR=1
MIN_GO_MINOR=22

if [ -t 1 ]; then
	C_B=$(printf '\033[1m'); C_G=$(printf '\033[32m'); C_Y=$(printf '\033[33m')
	C_R=$(printf '\033[31m'); C_N=$(printf '\033[0m')
else
	C_B=''; C_G=''; C_Y=''; C_R=''; C_N=''
fi
info() { printf '%s==>%s %s\n' "$C_B" "$C_N" "$*"; }
ok()   { printf '%s ok%s  %s\n' "$C_G" "$C_N" "$*"; }
warn() { printf '%swarning:%s %s\n' "$C_Y" "$C_N" "$*" >&2; }
die()  { printf '%serror:%s %s\n' "$C_R" "$C_N" "$*" >&2; exit 1; }
okd()  { [ "$DRY_RUN" = 1 ] || ok "$@"; }

usage() {
	cat <<USAGE
Usage: sh install.sh [options]

Builds $APP from source and installs it.

Options:
  --prefix DIR     install prefix (default: /usr/local as root or with --system,
                   otherwise \$HOME/.local)
  --bindir DIR     binary directory (default: PREFIX/bin)
  --mandir DIR     man directory    (default: PREFIX/share/man, or PREFIX/man if that is what exists)
  --system         install to /usr/local, using sudo or doas when needed
  --no-man         do not install the manual page
  --skip-build     do not build; install the existing bin/$APP
  --uninstall      remove a previous installation (your VMs and config are kept)
  -n, --dry-run    show what would be done without changing anything
  -y, --yes        do not ask for confirmation
  -h, --help       show this help

Environment: PREFIX, BINDIR and MANDIR work like the options above.

Needs: Go $MIN_GO_MAJOR.$MIN_GO_MINOR or newer (unless --skip-build). At run time $APP needs
qemu-system-<arch> and qemu-img.
USAGE
}

ACTION=install
ASSUME_YES=0
NO_MAN=0
SKIP_BUILD=0
SYSTEM=0
DRY_RUN=0
PREFIX=${PREFIX:-}
BINDIR=${BINDIR:-}
MANDIR=${MANDIR:-}

while [ $# -gt 0 ]; do
	case $1 in
	--prefix)  [ $# -ge 2 ] || die "--prefix needs a value"; PREFIX=$2; shift 2 ;;
	--prefix=*) PREFIX=${1#*=}; shift ;;
	--bindir)  [ $# -ge 2 ] || die "--bindir needs a value"; BINDIR=$2; shift 2 ;;
	--bindir=*) BINDIR=${1#*=}; shift ;;
	--mandir)  [ $# -ge 2 ] || die "--mandir needs a value"; MANDIR=$2; shift 2 ;;
	--mandir=*) MANDIR=${1#*=}; shift ;;
	--system)  SYSTEM=1; shift ;;
	--no-man)  NO_MAN=1; shift ;;
	--skip-build) SKIP_BUILD=1; shift ;;
	--uninstall) ACTION=uninstall; shift ;;
	-n|--dry-run) DRY_RUN=1; shift ;;
	-y|--yes)  ASSUME_YES=1; shift ;;
	-h|--help) usage; exit 0 ;;
	*) die "unknown option: $1 (try --help)" ;;
	esac
done

OS=$(uname -s)

if [ -z "$PREFIX" ]; then
	if [ "$SYSTEM" = 1 ] || [ "$(id -u)" = 0 ]; then
		PREFIX=/usr/local
	else
		[ -n "${HOME:-}" ] || die "HOME is not set; use --prefix"
		PREFIX=$HOME/.local
	fi
fi
[ -n "$BINDIR" ] || BINDIR=$PREFIX/bin
if [ -z "$MANDIR" ]; then
	if [ -d "$PREFIX/share/man/man1" ] || [ ! -d "$PREFIX/man/man1" ]; then
		MANDIR=$PREFIX/share/man
	else
		MANDIR=$PREFIX/man
	fi
fi

SRC=$(cd "$(dirname "$0")" && pwd)
cd "$SRC"

# ---- privilege handling: only the copy step may need it, never the build ----

nearest_writable() {
	nw_d=$1
	while [ ! -e "$nw_d" ]; do nw_d=$(dirname "$nw_d"); done
	[ -w "$nw_d" ]
}

PRIV=''
if ! nearest_writable "$BINDIR" || { [ "$NO_MAN" = 0 ] && ! nearest_writable "$MANDIR"; }; then
	if [ "$(id -u)" != 0 ]; then
		if command -v sudo >/dev/null 2>&1; then
			PRIV=sudo
		elif command -v doas >/dev/null 2>&1; then
			PRIV=doas
		else
			die "no write access to $BINDIR and neither sudo nor doas found (try --prefix \"\$HOME/.local\")"
		fi
	fi
fi

run() {
	if [ "$DRY_RUN" = 1 ]; then
		printf '  (dry-run) %s\n' "$*"
	else
		"$@"
	fi
}
prun() {
	if [ -n "$PRIV" ]; then
		run "$PRIV" "$@"
	else
		run "$@"
	fi
}

# ---- uninstall ----

if [ "$ACTION" = uninstall ]; then
	info "Removing $APP from $PREFIX"
	prun rm -f "$BINDIR/$APP" "$MANDIR/man1/$APP.1" "$MANDIR/man1/$APP.1.gz"
	okd "removed $BINDIR/$APP and its manual page"
	printf 'Your VMs and settings are untouched: ~/.local/share/qemu-tui and ~/.config/qemu-tui\n'
	exit 0
fi

# ---- sanity checks ----

[ -f go.mod ] && [ -d "cmd/$APP" ] || die "run this script from the $APP source directory (go.mod not found)"

go_hint() {
	case $OS in
	FreeBSD) printf 'pkg install go' ;;
	Darwin)  printf 'brew install go' ;;
	OpenBSD) printf 'pkg_add go' ;;
	NetBSD)  printf 'pkgin install go' ;;
	*)       printf 'see https://go.dev/dl/ or use your package manager (for example: sudo apt install golang)' ;;
	esac
}

pkg_hint() {
	case $OS in
	FreeBSD) printf 'pkg install qemu edk2-qemu-x64' ;;
	Darwin)  printf 'brew install qemu' ;;
	OpenBSD) printf 'pkg_add qemu' ;;
	NetBSD)  printf 'pkgin install qemu' ;;
	Linux)
		if command -v apt-get >/dev/null 2>&1; then
			printf 'sudo apt install qemu-system-x86 qemu-utils ovmf'
		elif command -v pacman >/dev/null 2>&1; then
			printf 'sudo pacman -S qemu-full edk2-ovmf'
		elif command -v dnf >/dev/null 2>&1; then
			printf 'sudo dnf install qemu-kvm qemu-img edk2-ovmf'
		elif command -v zypper >/dev/null 2>&1; then
			printf 'sudo zypper install qemu-x86 qemu-tools qemu-ovmf-x86_64'
		elif command -v apk >/dev/null 2>&1; then
			printf 'doas apk add qemu-system-x86_64 qemu-img'
		elif command -v xbps-install >/dev/null 2>&1; then
			printf 'sudo xbps-install qemu'
		else
			printf 'install QEMU and qemu-img with your package manager'
		fi ;;
	*) printf 'install QEMU and qemu-img with your package manager' ;;
	esac
}

# ---- build ----

if [ "$SKIP_BUILD" = 0 ]; then
	command -v go >/dev/null 2>&1 || die "Go is required to build $APP: $(go_hint)"
	GO_VER=$(go version | sed -n 's/.*go\([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\1 \2/p')
	[ -n "$GO_VER" ] || die "could not read the Go version from: $(go version)"
	GO_MAJOR=${GO_VER% *}
	GO_MINOR=${GO_VER#* }
	if [ "$GO_MAJOR" -lt "$MIN_GO_MAJOR" ] || { [ "$GO_MAJOR" -eq "$MIN_GO_MAJOR" ] && [ "$GO_MINOR" -lt "$MIN_GO_MINOR" ]; }; then
		die "Go $MIN_GO_MAJOR.$MIN_GO_MINOR or newer is required (found $GO_MAJOR.$GO_MINOR)"
	fi
	ok "Go $GO_MAJOR.$GO_MINOR"
	info "Building $APP"
	if [ ! -f go.sum ]; then
		run go mod tidy
	fi
	mkdir -p bin 2>/dev/null || true
	run go build -ldflags "-s -w" -o "bin/$APP" "./cmd/$APP"
else
	[ "$DRY_RUN" = 1 ] || [ -x "bin/$APP" ] || die "--skip-build needs an existing bin/$APP"
	info "Skipping build (using bin/$APP)"
fi

# ---- confirm ----

printf '\n'
info "Installation plan"
printf '  binary : %s/%s\n' "$BINDIR" "$APP"
if [ "$NO_MAN" = 0 ] && [ -f "man/$APP.1" ]; then
	printf '  man    : %s/man1/%s.1\n' "$MANDIR" "$APP"
fi
[ -z "$PRIV" ] || printf '  (will use %s for the copy step)\n' "$PRIV"
if [ "$ASSUME_YES" = 0 ] && [ "$DRY_RUN" = 0 ] && [ -t 0 ]; then
	printf 'Continue? [Y/n] '
	read -r ANSWER || ANSWER=n
	case $ANSWER in
	[nN]*) die "cancelled" ;;
	esac
fi

# ---- install ----

prun install -d "$BINDIR"
prun install -m 0755 "bin/$APP" "$BINDIR/$APP"
okd "installed $BINDIR/$APP"

if [ "$NO_MAN" = 0 ]; then
	if [ -f "man/$APP.1" ]; then
		prun install -d "$MANDIR/man1"
		prun install -m 0644 "man/$APP.1" "$MANDIR/man1/$APP.1"
		okd "installed manual page"
	else
		warn "man/$APP.1 not found; skipping the manual page"
	fi
fi

# ---- post-install advice (never fatal) ----

if [ "$DRY_RUN" = 0 ]; then
	case ":$PATH:" in
	*":$BINDIR:"*) ;;
	*) warn "$BINDIR is not in your PATH. Add this to your shell profile:
         export PATH=\"$BINDIR:\$PATH\"" ;;
	esac
	if [ "$NO_MAN" = 0 ] && [ -f "man/$APP.1" ] && command -v man >/dev/null 2>&1; then
		if ! man -w "$APP" >/dev/null 2>&1; then
			warn "'man $APP' does not find the page yet. Try:  export MANPATH=\"$MANDIR:\$(manpath 2>/dev/null)\""
		fi
	fi
fi

MISSING=''
for tool in qemu-system-x86_64 qemu-img; do
	command -v "$tool" >/dev/null 2>&1 || MISSING="$MISSING $tool"
done
if [ -n "$MISSING" ]; then
	warn "not found:$MISSING"
	printf '         install QEMU with: %s\n' "$(pkg_hint)" >&2
fi
if [ "$OS" = Linux ]; then
	if [ ! -e /dev/kvm ]; then
		printf 'note: /dev/kvm not found; VMs will run without hardware acceleration (slow).\n'
	elif [ ! -w /dev/kvm ]; then
		warn "no access to /dev/kvm; add your user to the 'kvm' group and log in again"
	fi
elif [ "$OS" = FreeBSD ]; then
	printf 'note: FreeBSD has no KVM; guests run with software emulation (TCG).\n'
fi

printf '\n'
if [ "$DRY_RUN" = 1 ]; then
	ok "dry run finished; nothing was changed"
else
	ok "done. Run: $APP    Manual: man $APP    Uninstall: sh install.sh --uninstall"
fi
