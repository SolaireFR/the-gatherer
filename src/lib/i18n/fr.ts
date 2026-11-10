import type { Translation } from '@solairefr/gosvelte-front/i18n/i18n.svelte.js';
import type en from './en';

export default {
	nav: { admin: 'Administration' },
	news: {
		title: 'News',
		description: "Les news de la veille, récupérées chaque nuit à minuit par l'IA : générales, tech et alternatives aux technologies utilisées.",
		sections: { generales: 'Générales', tech: 'Tech', alternatives: 'Alternatives' },
		section: 'Rubrique',
		allSections: 'Toutes les rubriques',
		category: 'Catégorie',
		allCategories: 'Toutes les catégories',
		count: (p) => `${p.count} news`,
		empty: 'Aucune news pour cette sélection.',
		noDays: 'Aucune news pour l’instant : la première récupération a lieu à minuit, ou se lance depuis le',
		loadFailed: 'Chargement des news impossible : {error}'
	},
	favorites: {
		title: 'Favoris',
		description: 'Toutes les news marquées en favori.',
		count: (p) => `${p.count} favori${Number(p.count) > 1 ? 's' : ''}`,
		empty: 'Aucun favori : cliquez sur l’étoile d’une news pour l’ajouter.',
		add: 'Ajouter aux favoris : {title}',
		remove: 'Retirer des favoris : {title}',
		toggleFailed: 'Mise à jour du favori impossible : {error}'
	},
	topics: {
		title: 'Configuration',
		description: "Technologies utilisées et sujets bloqués, envoyés à l'IA à chaque récupération.",
		intro: "Envoyés à l'IA à chaque récupération (chaque nuit à minuit). Les catégories, elles, sont choisies librement par l'IA.",
		tech: {
			title: 'Technologies utilisées',
			hint: "L'IA cherche leurs alternatives récentes. Aucune = pas de rubrique Alternatives.",
			placeholder: 'Ex : Go, Docker, PostgreSQL…',
			empty: 'Aucune technologie.'
		},
		exclude: {
			title: 'Sujets bloqués',
			hint: 'Écartés de toutes les rubriques.',
			placeholder: 'Ex : crypto, politique, sport…',
			empty: 'Aucun sujet bloqué.'
		},
		label: 'Libellé',
		add: 'Ajouter',
		added: '« {label} » ajouté.',
		deleted: '« {label} » retiré.',
		deleteName: 'Retirer {label}',
		loadFailed: 'Chargement de la configuration impossible : {error}',
		saveFailed: 'Ajout impossible : {error}',
		deleteFailed: 'Suppression impossible : {error}'
	},
	journal: {
		title: 'Journal',
		description: "Historique des récupérations de news : prompt envoyé à l'IA et résultat.",
		intro: "Chaque récupération (chaque nuit à minuit, ou lancée à la main) demande à l'IA les news de la veille.",
		start: 'Récupérer les news de la veille',
		started: 'Récupération du {day} lancée : rafraîchir dans quelques minutes.',
		startFailed: 'Lancement impossible : {error}',
		loadFailed: 'Chargement du journal impossible : {error}',
		empty: 'Aucune récupération pour l’instant.',
		date: 'Date',
		day: 'News du',
		model: 'Modèle',
		saved: 'Enregistrées',
		status: 'Résultat',
		ok: 'OK',
		failed: 'Échec',
		prompt: 'Prompt',
		promptTitle: 'Prompt du {date}'
	},
	admin: {
		groups: { news: 'News' },
		setting: {
			MISTRAL_MODEL: 'Modèle Mistral interrogé pour les news',
			MISTRAL_TEMPERATURE: 'Température du modèle, de 0 (factuel) à 1.5'
		}
	},
	errors: {
		news: {
			dayInvalid: 'Jour attendu au format AAAA-MM-JJ',
			notFound: 'News introuvable',
			topicKindInvalid: 'Liste inconnue',
			topicLabelInvalid: 'Libellé requis ({max} caractères maximum)',
			topicExists: '« {label} » est déjà dans la liste',
			topicNotFound: 'Entrée introuvable',
			fetchRunning: 'Une récupération est déjà en cours'
		}
	}
} satisfies Translation<typeof en>;
