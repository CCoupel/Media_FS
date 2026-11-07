# COMMON — adaptations Media_FS

Valeurs projet des placeholders laissés non substitués dans `COMMON.template.md` (et `DEVELOPMENT.template.md` pour
`{VERSION_READ_CMD}`). Media_FS est une application desktop : le « serveur » est le processus `mediafs` local.

| Placeholder | Section | Valeur Media_FS |
|-------------|---------|-----------------|
| `{BUILD_VALIDATE_CMD}` | 2.2 | `test -s dist/mediafs && bash scripts/ci.sh` (build Linux natif du conteneur ; le `.exe` Windows est produit par la CI ou à la main — `CLAUDE.md` § Build & Run) |
| `{SERVER_STOP_CMD}` | 3.1 | Tray : menu « Quitter » (démonte proprement). Headless : `Ctrl+C` sur `mediafs mount` (démontage WinFSP/FUSE). Jamais de kill forcé : un lecteur laissé monté doit être démonté à la main |
| `{SERVER_RESTART_CMD}` | 3.2 | arrêt ci-dessus, puis `./dist/mediafs` (tray) ou `./dist/mediafs mount` (headless) |
| `{SERVER_VERIFY_CMD}` | 3.3 | le lecteur virtuel est monté et la racine liste les serveurs configurés ; webui de configuration joignable |
| `{VERSION_READ_CMD}` | 5.6 | `git describe --tags --abbrev=0` (release) — `internal/version/version.go` vaut `dev`, la version est injectée par `-ldflags` au build |
