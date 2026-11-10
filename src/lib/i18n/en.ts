// Textes du projet en anglais : langue de référence (clés acceptées par t(), textes de repli des autres langues).
// Ajouter une langue = ajouter src/lib/i18n/<code>.ts (ex : de.ts) : export default { … } satisfies Translation<typeof en>;
// Textes du moteur (common.*, auth.*, files.title…) : utilisables directement dans t(), surchargeables ici.
import type { Dictionary } from '@solairefr/gosvelte-front/i18n/i18n.svelte.js';

const en = {
	nav: { admin: 'Administration' },
	news: {
		title: 'News',
		description: "Yesterday's news, gathered every night at midnight by the AI: general, tech, and alternatives to the technologies you use.",
		sections: { generales: 'General', tech: 'Tech', alternatives: 'Alternatives' },
		section: 'Section',
		allSections: 'All sections',
		category: 'Category',
		allCategories: 'All categories',
		count: (p) => `${p.count} news`,
		empty: 'No news for this selection.',
		noDays: 'No news yet: the first fetch happens at midnight, or can be started from the',
		loadFailed: 'Could not load the news: {error}'
	},
	favorites: {
		title: 'Favorites',
		description: 'All the news marked as favorite.',
		count: (p) => `${p.count} favorite${Number(p.count) === 1 ? '' : 's'}`,
		empty: 'No favorites yet: click the star of a news item to add it.',
		add: 'Add to favorites: {title}',
		remove: 'Remove from favorites: {title}',
		toggleFailed: 'Could not update the favorite: {error}'
	},
	topics: {
		title: 'Configuration',
		description: 'Technologies you use and blocked topics, sent to the AI on every fetch.',
		intro: 'Sent to the AI on every fetch (every night at midnight). Categories are chosen freely by the AI.',
		tech: {
			title: 'Technologies used',
			hint: 'The AI looks for recent alternatives to them. None = no Alternatives section.',
			placeholder: 'E.g. Go, Docker, PostgreSQL…',
			empty: 'No technology.'
		},
		exclude: {
			title: 'Blocked topics',
			hint: 'Left out of every section.',
			placeholder: 'E.g. crypto, politics, sports…',
			empty: 'No blocked topic.'
		},
		label: 'Label',
		add: 'Add',
		added: '« {label} » added.',
		deleted: '« {label} » removed.',
		deleteName: 'Remove {label}',
		loadFailed: 'Could not load the configuration: {error}',
		saveFailed: 'Could not add: {error}',
		deleteFailed: 'Could not remove: {error}'
	},
	journal: {
		title: 'Journal',
		description: 'History of the news fetches: prompt sent to the AI and result.',
		intro: 'Each fetch (every night at midnight, or started by hand) asks the AI for the news of the previous day.',
		start: "Fetch yesterday's news",
		started: 'Fetch of {day} started: refresh in a few minutes.',
		startFailed: 'Could not start the fetch: {error}',
		loadFailed: 'Could not load the journal: {error}',
		empty: 'No fetch yet.',
		date: 'Date',
		day: 'News of',
		model: 'Model',
		saved: 'Saved',
		status: 'Result',
		ok: 'OK',
		failed: 'Failed',
		prompt: 'Prompt',
		promptTitle: 'Prompt of {date}'
	},
	admin: {
		groups: { news: 'News' },
		setting: {
			MISTRAL_MODEL: 'Mistral model asked for the news',
			MISTRAL_TEMPERATURE: 'Model temperature, from 0 (factual) to 1.5'
		}
	},
	errors: {
		news: {
			dayInvalid: 'Day expected as YYYY-MM-DD',
			notFound: 'News not found',
			topicKindInvalid: 'Unknown list',
			topicLabelInvalid: 'Label required ({max} characters maximum)',
			topicExists: '« {label} » is already in the list',
			topicNotFound: 'Entry not found',
			fetchRunning: 'A fetch is already running'
		}
	}
} satisfies Dictionary;

export default en;

declare module '@solairefr/gosvelte-front/i18n/i18n.svelte.js' {
	interface Register {
		dictionary: typeof en;
	}
}
