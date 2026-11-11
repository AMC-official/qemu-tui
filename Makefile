PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin
MANDIR ?= $(PREFIX)/share/man

.PHONY: build test vet run install uninstall man-check

build:
	go build -ldflags "-s -w" -o bin/qemu-tui ./cmd/qemu-tui

test:
	go test ./...

vet:
	go vet ./...

run:
	go run ./cmd/qemu-tui

install: build
	install -d $(DESTDIR)$(BINDIR) $(DESTDIR)$(MANDIR)/man1
	install -m 0755 bin/qemu-tui $(DESTDIR)$(BINDIR)/qemu-tui
	install -m 0644 man/qemu-tui.1 $(DESTDIR)$(MANDIR)/man1/qemu-tui.1

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/qemu-tui $(DESTDIR)$(MANDIR)/man1/qemu-tui.1

# Needs mandoc (default on FreeBSD) or: groff -man -Tutf8 -ww -z man/qemu-tui.1
man-check:
	mandoc -Tlint man/qemu-tui.1
