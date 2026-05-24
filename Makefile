MSG          ?= update
PUB_MSG      ?=
PUBLIC_DIR   := ../logmcp-public
DEV_HOST     := user@switchbox-dev.gpt4voice.de
PROD1_HOST   := user@switchbox-prod1.gpt4voice.de
BINARY       := logmcp
INSTALL_TO   := /usr/local/bin/$(BINARY)
VERSION      := $(shell git describe --tags 2>/dev/null | sed 's/^v//' | grep . || echo "0.1.0")
ARCH         := $(shell dpkg --print-architecture 2>/dev/null || echo "amd64")
DEB_STAGING  := .deb-staging
DEB_NAME     := $(BINARY)_$(VERSION)_$(ARCH).deb

.PHONY: help build deb deploy-dev deploy-prod1 deploy-deb-dev setup-dev commit push public-sync public-push

help:
	@echo "LogMCP — verfügbare Targets:"
	@echo ""
	@echo "  build             Binary lokal bauen (./logmcp)"
	@echo "  deb               Debian-Paket bauen  → $(DEB_NAME)"
	@echo "  deploy-dev        Binary bauen und auf $(DEV_HOST) deployen"
	@echo "  deploy-prod1      Binary bauen und auf $(PROD1_HOST) deployen"
	@echo "  deploy-deb-dev    .deb bauen und auf $(DEV_HOST) installieren"
	@echo "  setup-dev         Binary deployen + interaktiven Setup-Wizard auf dem Server starten"
	@echo "                    ('sudo logmcp setup' fragt: User anlegen? Gruppe adm? TLS? ...)"
	@echo "  commit            Alle Änderungen committen  (MSG=... überschreibt die Nachricht)"
	@echo "  push              commit + git push origin main  (MSG=... optional)"
	@echo "  public-sync       Dev → Public synchronisieren (rsync, ohne private Dateien)"
	@echo "  public-push       Public committen + pushen     (PUB_MSG=\"...\" erforderlich)"
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

deploy-deb-dev: deb
	scp $(DEB_NAME) $(DEV_HOST):~/$(DEB_NAME)
	ssh $(DEV_HOST) "sudo apt-get install -y ~/$(DEB_NAME) && rm ~/$(DEB_NAME)"
	@echo "Paket installiert auf $(DEV_HOST)"

deploy-dev: build
	scp $(BINARY) $(DEV_HOST):~/$(BINARY).new
	ssh $(DEV_HOST) "sudo mv ~/$(BINARY).new $(INSTALL_TO) && sudo chmod +x $(INSTALL_TO)"
	@echo "Deployed to $(DEV_HOST):$(INSTALL_TO)"

deploy-prod1: build
	scp $(BINARY) $(PROD1_HOST):~/$(BINARY).new
	ssh $(PROD1_HOST) "sudo mv ~/$(BINARY).new $(INSTALL_TO) && sudo chmod +x $(INSTALL_TO)"
	@echo "Deployed to $(PROD1_HOST):$(INSTALL_TO)"

# Einmalig ausführen: Binary deployen und Setup-Wizard auf dem Server starten.
setup-dev: deploy-dev
	ssh -t $(DEV_HOST) "sudo $(INSTALL_TO) setup"

commit:
	git add -A
	git diff --cached --quiet || git commit -m "$(MSG)"

push: commit
	git push -u origin main

public-sync:
	rsync -av --delete \
	  --exclude='.git/' \
	  --exclude='CLAUDE.md' \
	  --exclude='.claude/' \
	  --exclude='TODO.md' \
	  --exclude='tasks/' \
	  --exclude='anforderungen.md' \
	  --exclude='doc-priv/' \
	  --exclude='.vscode/' \
	  --exclude='logmcp' \
	  --exclude='*.deb' \
	  --exclude='.deb-staging/' \
	  ./ $(PUBLIC_DIR)/

public-push:
	@test -n "$(PUB_MSG)" || (echo "Fehler: PUB_MSG fehlt.  Aufruf: make public-push PUB_MSG='...'"; exit 1)
	cd $(PUBLIC_DIR) && git add -A && git commit -m "$(PUB_MSG)" && git push -u origin main
