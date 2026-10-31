# deploy.prod — adaptation Media_FS

PROD = **GitHub Release** créée par `.github/workflows/release.yml` sur push du tag `vX.Y.Z`
(binaires Windows amd64, Linux amd64, paquets DEB/RPM via nfpm).

- Il n'y a rien à installer sur un serveur : le « déploiement » consiste à vérifier que la release
  `vX.Y.Z` existe et contient tous les assets attendus (`gh release view vX.Y.Z --json assets`).
- Ignorer les étapes SSH / hôte du template `deploy.prod.template.md`.
