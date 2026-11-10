# Changelog du site

<!-- Nouveautés propres à CE site (celles de gosvelte sont dans le CHANGELOG.md de @solairefr/gosvelte-front : ne pas les recopier ici).
     Même format que ce CHANGELOG.md : un titre de niveau 2 "AAAA-MM-JJ" puis une ligne de liste par changement.
     Seules les lignes de liste commençant par "[public]" sont affichées par le bouton « Nouveautés » du menu,
     mélangées à celles de gosvelte (plus récentes en haut). Ajoutez vos dates sous ce commentaire. -->

## 2026-11-10
- [public] News de la veille demandées chaque nuit à minuit à l'IA (recherche web) : générales, tech et alternatives aux technologies utilisées.
- [public] Favoris : étoile sur chaque news et page Favoris.
- Prompt des news modifiable dans /admin > Paramètres (`NEWS_PROMPT`, variables `{date}`, `{technologies}`, `{exclusions}`).
- Projet migré sur gosvelte (Go + SvelteKit) : les anciennes APIs de news (Dev.to, GNews, NewsAPI, CurrentsAPI) sont retirées.
