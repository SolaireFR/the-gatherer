// Point d'entrée de l'application : drivers, modules métier et variables du projet, puis gosvelte.Start.
// Seuls les packages importés sont compilés : garder UNE base de données, UN provider d'authentification et AU PLUS UN stockage de fichiers.
// Par défaut : SQLite, JWT local et dossier local (aucun service à lancer) ; MySQL, Keycloak et MinIO : gosvelte services up.
package main

import (
	// Fuseaux horaires embarqués : l'image Docker (scratch) n'en a pas, TZ=Europe/Paris en a besoin pour la récupération
	// des news à minuit, heure de Paris
	_ "time/tzdata"

	gosvelte "github.com/SolaireFR/gosvelte/gosvelte-core"
	"github.com/SolaireFR/gosvelte/gosvelte-core/config"

	// Base de données
	_ "github.com/SolaireFR/gosvelte/gosvelte-core/database/sqlite"

	// Authentification
	_ "github.com/SolaireFR/gosvelte/gosvelte-core/auth/basic"

	// Administration : page /admin réservée au rôle admin et invisible pour les autres (404), mot de passe redemandé
	// (ADMIN_SUDO_MINUTES), IP filtrées (ADMIN_ALLOWED_IPS) ; tableau de bord, données, réglages chiffrés (SETTINGS_SECRET_KEY),
	// clés d'API (clé Mistral de l'API externe « mistral »), journal
	_ "github.com/SolaireFR/gosvelte/gosvelte-core/admin"

	// Veille : news de la veille demandées chaque jour à minuit à Mistral (recherche web), favoris, configuration, journal
	_ "app/back/news"
)

// env : variables propres au projet (vérifiées au démarrage par config.Validate) : required:"true" pour les rendre
// obligatoires, default:"..." pour une valeur par défaut, à documenter dans .env.example ; les PUBLIC_* sont aussi
// exposées au front ($generated/env)
var env struct {
	PUBLIC_PROJECT_NAME string `description:"Nom du projet (public)" required:"true"`
}

func init() {
	config.Register(&env)
}

func main() {
	gosvelte.Start() // front embarqué par « gosvelte build » ; avec un simple go run ./back, l'API seule
}
