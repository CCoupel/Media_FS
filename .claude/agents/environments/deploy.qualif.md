# deploy.qualif — adaptation Media_FS

QUALIF est **local** (pas d'hôte distant, pas de SSH) : Media_FS est une application desktop.

- Artefact : `build/qualif_v<X.Y.Z>/mediafs-<X.Y.Z.a>.exe` (promu par `/publish qualif`).
- « Déploiement » = arrêter l'instance `mediafs` en cours, copier l'artefact dans `dist/`, relancer
  (`./dist/mediafs.exe` mode tray, ou `./dist/mediafs.exe mount` headless).
- Vérification : le lecteur virtuel est monté et la bibliothèque d'au moins un serveur configuré est navigable.
- Ignorer les étapes SSH / registre du template `deploy.qualif.template.md`.
