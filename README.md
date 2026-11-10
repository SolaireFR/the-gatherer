# gosvelte-template

Projet gosvelte : le code métier dans `back/` (Go) et `src/` (SvelteKit), plus trois fichiers pointeurs. Le moteur
(`github.com/SolaireFR/gosvelte/gosvelte-core`, `@solairefr/gosvelte-front`) et toute la configuration (SvelteKit, Vite,
Capacitor, Docker) sont fournis par la commande `gosvelte`. Règles et briques : `AGENTS.md`.

Ce dossier est le modèle copié par `gosvelte new` ; il se lance aussi tel quel, branché sur les sources du dépôt.

```
back/main.go        drivers, modules métier, variables du projet, gosvelte.Start()
back/example/       exemple CRUD Voitures + notifications
src/routes/         pages SvelteKit
src/lib/            layout.ts (theme.css, changelog.md : optionnels)
src/static/         fichiers servis tels quels (logo.svg) : optionnel
go.mod              module app + gosvelte-core (et la commande : go tool gosvelte)
package.json        gosvelte-front (+ svelte, @sveltejs/kit pour l'éditeur) et scripts dev / build / check
tsconfig.json       étend .gosvelte/tsconfig.json (alias $lib, $generated, types des routes)
.env.example        configuration documentée (copiée en .env par gosvelte new)
```

Générés (non versionnés) : `.gosvelte/`, `node_modules/`, `data/`. À versionner : `go.sum`, `pnpm-lock.yaml`.

## Démarrer

Prérequis : Go 1.27.3+, Node 24, pnpm (`npm install -g pnpm`), Docker (services, image).

```sh
gosvelte dev    # front http://localhost:4444 (/api relayé) · API http://localhost:3333 · connexion admin / ADMIN_PASSWORD du .env (aléatoire, créé par gosvelte new)
```

Drivers par défaut (`back/main.go`) : SQLite, JWT local, fichiers dans `./data` : aucun service à lancer.
MySQL, Keycloak ou MinIO : décommenter leur import, puis `gosvelte services up`.
Administration : http://localhost:4444/admin (rôle admin, mot de passe redemandé, 404 pour les autres ; `.env` : ADMINISTRATION, RÉGLAGES).

## Commandes

`gosvelte <commande>` (commande installée) ou `go tool gosvelte <commande>` (version de gosvelte-core de `go.mod`,
sans installation) ; `pnpm dev`, `pnpm build`, `pnpm check` en raccourcis.

| Commande | Rôle |
| --- | --- |
| `dev` | génère `.gosvelte/`, lance Vite (:4444) et l'app Go (:3333), recompilée à chaque modification de `back/`, `go.mod`, `.env` ou du moteur local |
| `build` | front (+ robots.txt, sitemap.xml) embarqué dans `.gosvelte/bin/app[.exe]`, à lancer depuis la racine (`.env`, `./data`) |
| `start` | `build` puis lance l'app sur http://localhost:3333 |
| `generate` | régénère `.gosvelte/` (env.ts, api.ts, types SvelteKit) : après un clone ou un pull, pour l'IDE |
| `check` | svelte-check + go vet |
| `update [version]` | moteur local : reconstruit et réinstalle gosvelte-front ; sinon gosvelte-core + gosvelte-front à la version (défaut : latest) |
| `docker [args…]` | image Docker (scratch + binaire statique) nommée d'après le dossier ; `args` passés à `docker build` ; `PUBLIC_*` lus dans `.env.example` |
| `services [up\|down]` | services de développement MySQL, MinIO, Keycloak (docker compose) |
| `mobile [sync\|dev\|open\|add]` | app Capacitor (paquets `@capacitor/*` à ajouter au `package.json`) |
| `version` | version de la commande |

Sans Node : `go run ./back` lance l'API seule.

## Ajouter une dépendance

- Go : `go get <module>` à la racine. Après un changement de driver dans `back/main.go` : `go mod tidy` (relancé seul par `dev`).
- npm : `pnpm add <paquet>`. L'outillage (Vite, svelte-check, Tailwind…) vient de gosvelte-front : ne pas l'ajouter.

## Pourquoi ces trois fichiers

Les éditeurs ne lisent que ce qui est à la racine : gopls a besoin de `go.mod`, TypeScript et le serveur de langage Svelte
de `tsconfig.json` et de `node_modules/`. Tout le reste (configs SvelteKit / Vite, `api.ts`, builds) est dans `.gosvelte/` ;
le front est embarqué dans le binaire par `go build -overlay`, sans fichier généré dans `back/`.

## Moteur local et versions publiées

Dans le dépôt gosvelte (et dans les projets créés par `gosvelte new`), le projet est branché sur les sources :
`replace github.com/SolaireFR/gosvelte/gosvelte-core => ../gosvelte-core` (`go.mod`, lu directement : la commande
`go tool gosvelte` et l'app sont recompilées depuis les sources) et `"@solairefr/gosvelte-front": "file:../gosvelte-front"`
(`package.json`, copie installée : `gosvelte update` la reconstruit).

Versions publiées : retirer la ligne `replace` puis `go get github.com/SolaireFR/gosvelte/gosvelte-core@vX.Y.Z`,
et `"@solairefr/gosvelte-front": "X.Y.Z"` (même version) dans `package.json`.
