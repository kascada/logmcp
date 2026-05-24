BINARY      := logmcp
INSTALL_TO  := /usr/local/bin/$(BINARY)
VERSION     := $(shell git describe --tags 2>/dev/null | sed 's/^v//' | grep . || echo "0.1.0")
ARCH        := $(shell dpkg --print-architecture 2>/dev/null || echo "amd64")
DEB_STAGING := .deb-staging
DEB_NAME    := $(BINARY)_$(VERSION)_$(ARCH).deb

.PHONY: help build deb

help:
	@echo "LogMCP — verfügbare Targets:"
	@echo ""
	@echo "  build   Binary lokal bauen (./logmcp)"
	@echo "  deb     Debian-Paket bauen → $(DEB_NAME)"
	@echo ""

.DEFAULT_GOAL := help

build:
	go build -o $(BINARY) .

deb: build
	rm -rf $(DEB_STAGING)
	mkdir -p $(DEB_STAGING)/DEBIAN
	mkdir -p $(DEB_STAGING)/usr/local/bin
	mkdir -p $(DEB_STAGING)/lib/systemd/system
	mkdir -p $(DEB_STAGING)/etc/logmcp
	cp $(BINARY) $(DEB_STAGING)/usr/local/bin/$(BINARY)
	chmod 755 $(DEB_STAGING)/usr/local/bin/$(BINARY)
	cp packaging/logmcp.service $(DEB_STAGING)/lib/systemd/system/logmcp.service
	sed "s/@@VERSION@@/$(VERSION)/; s/@@ARCH@@/$(ARCH)/" packaging/control > $(DEB_STAGING)/DEBIAN/control
	cp packaging/postinst $(DEB_STAGING)/DEBIAN/postinst
	cp packaging/prerm    $(DEB_STAGING)/DEBIAN/prerm
	cp packaging/postrm   $(DEB_STAGING)/DEBIAN/postrm
	chmod 755 $(DEB_STAGING)/DEBIAN/postinst $(DEB_STAGING)/DEBIAN/prerm $(DEB_STAGING)/DEBIAN/postrm
	dpkg-deb --build --root-owner-group $(DEB_STAGING) $(DEB_NAME)
	rm -rf $(DEB_STAGING)
	@echo "Paket: $(DEB_NAME)"
