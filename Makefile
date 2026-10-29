.PHONY: setup dev migrate

setup:
	node scripts/dev.mjs setup

dev:
	node scripts/dev.mjs dev

migrate:
	node scripts/dev.mjs migrate
