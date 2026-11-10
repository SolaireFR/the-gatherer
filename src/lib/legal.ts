// Éditeur du site, hébergeur et durées : à compléter avant la mise en ligne (pages /privacy et /legal : champ vide affiché
// « à compléter »). RGPD et loi pour la confiance dans l'économie numérique (mentions légales) : cf. RGPD.md à la racine.
import type { LegalInfo } from '@solairefr/gosvelte-front/legal/legal.js';

export const legal: LegalInfo = {
	name: '',
	company: '', // forme juridique, capital, RCS / SIRET (société)
	address: '',
	email: '',
	director: '',
	host: { name: '', address: '', phone: '' },
	// Journaux techniques (adresses IP) conservés par l'hébergeur ou Docker, ex. « 6 mois » (CNIL : 6 mois à 1 an)
	logRetention: '',
	// Ex. « Bored API (exemple) : États-Unis » ; sinon « aucun »
	transfers: '',
	updated: '2026-11-09'
};
