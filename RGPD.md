# RGPD : ce que fait le socle, ce qui reste à faire

Repères vérifiés en novembre 2026. Ce document n'est pas un avis juridique : faites valider la mise en ligne par la
personne chargée de la conformité (ou un DPO).

## Fourni par le socle

| Obligation | Ce qui est en place |
| --- | --- |
| Information (art. 13) | Pages `/privacy` (sections générées d'après la configuration réelle : durées, quota, services tiers) et `/legal` ; liens dans le pied de page, sous le formulaire d'inscription et dans le profil |
| Accès et portabilité (art. 15, 20) | Profil > « Télécharger mes données » : fichier JSON (`GET /api/auth/me/export`) |
| Rectification (art. 16) | Profil : identifiant, e-mail, mot de passe |
| Effacement (art. 17) | Profil > « Supprimer mon compte » : compte, fichiers, notifications et données déclarées par le projet |
| Conservation limitée (art. 5) | Journal d'administration : `ADMIN_AUDIT_RETENTION_MONTHS` (12 mois par défaut) ; comptes inactifs : `AUTH_INACTIVE_MONTHS` (provider basic, désactivé par défaut) ; notifications : 50 par personne |
| Traceurs (art. 82 loi Informatique et Libertés) | Uniquement des cookies et du stockage local strictement nécessaires ou choisis par la personne (liste dans `/privacy`) : aucun bandeau de consentement requis |
| Sécurité (art. 32) | HTTPS et HSTS, mots de passe hachés (bcrypt) et robustes (CNIL, délibération 2022-100, cas n°2), tentatives de connexion limitées, réglages et clés d'API chiffrés, administration restreinte et journalisée |

## À faire avant la mise en ligne

1. **Remplir `src/lib/legal.ts`** : éditeur, directeur de la publication, hébergeur, durée des journaux, transferts hors
   UE. Les champs vides sont marqués « à compléter » dans les pages.
2. **Déclarer toute donnée liée à un compte** avec `auth.RegisterPersonalData` (export, effacement), comme dans
   `back/example/voiture-rgpd.go`, et ajouter ses textes `privacy.sections.<nom>.*` dans `src/lib/i18n/*.ts`.
3. **Journaux techniques** (sortie standard : IP, requêtes) : rotation et conservation de 6 mois à 1 an, par exemple
   Docker `--log-opt max-size=50m --log-opt max-file=10`, ou journald `MaxRetentionSec=6month`.
4. **Sauvegardes** : chiffrées, avec une durée limitée. Une donnée effacée disparaît des sauvegardes à leur échéance.
5. **Sous-traitants** (art. 28) : contrat avec l'hébergeur, le stockage de fichiers et chaque API externe (`apis`).
   Pour un transfert hors UE, vérifier la garantie (Data Privacy Framework, clauses contractuelles types) et la décrire
   dans `legal.transfers`.
6. **Comptes inactifs** : choisir `AUTH_INACTIVE_MONTHS` (par exemple 24 ou 36 mois) dans /admin > Paramètres. La durée
   apparaît alors dans `/privacy`.
7. **Ajout de mesure d'audience, de publicité, de vidéos ou de cartes tierces** : consentement préalable obligatoire
   (sauf mesure d'audience exemptée aux conditions de la CNIL). Il faut alors un bandeau, et compléter `/privacy`.
8. **Registre des traitements** (art. 30) : partir du modèle ci-dessous.
9. **Violation de données** : notifier la CNIL sous 72 h (notifications.cnil.fr), informer les personnes en cas de
   risque élevé, et tenir un registre des violations.
10. **Keycloak** (si utilisé) : politique de mot de passe du realm (au moins 12 caractères de 4 types, ou équivalent),
    durée des sessions, et suppression des comptes inactifs (non automatique).

## Modèle de registre des traitements

| Traitement | Données | Finalité | Base légale | Conservation | Destinataires |
| --- | --- | --- | --- | --- | --- |
| Comptes | Identifiant, e-mail, mot de passe haché, rôle, dates | Gestion des comptes | Contrat | Durée du compte (`AUTH_INACTIVE_MONTHS`) | Administrateurs, hébergeur |
| Fichiers | Fichiers et leurs informations | Stockage et partage | Contrat | Jusqu'à leur suppression | Administrateurs, hébergeur, stockage |
| Notifications | Messages | Information | Contrat | 50 plus récentes | Hébergeur |
| Journal d'administration | Identifiant, IP, action | Sécurité | Intérêt légitime | `ADMIN_AUDIT_RETENTION_MONTHS` | Administrateurs |
| Journaux techniques | IP, requêtes, identifiant | Sécurité | Intérêt légitime | 6 mois à 1 an | Hébergeur |
| APIs externes | Selon les appels | À préciser | À préciser | À préciser | Fournisseurs des APIs |

## Références

- CNIL : recommandation sur les mots de passe (délibération 2022-100) et sur la journalisation (délibération 2021-122).
- CNIL : lignes directrices et recommandation « cookies et autres traceurs » (modifiées en 2026).
- Digital Omnibus (UE) : en négociation en novembre 2026, non adopté ; les règles sur les traceurs restent celles
  ci-dessus.
