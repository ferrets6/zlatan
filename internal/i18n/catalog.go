// SPDX-License-Identifier: AGPL-3.0-or-later

package i18n

// catalog is every phrase the wizard can show. A phrase earns its place by
// being something the person cannot work out from the screen itself: a state,
// a number, an action, or the one fact about Google that explains the whole
// Photos route. Anything else was cut.
//
// A test asserts that every language has exactly the same keys.
var catalog = map[Lang]map[string]string{
	EN: {
		"lang.name":   "English",
		"lang.switch": "Language",

		"page.title":      "Move your things off Google",
		"nothing.deleted": "Nothing is deleted from Google.",
		"tab.close":       "The work continues if you close this tab.",

		"drive.title":  "Files and folders",
		"photos.title": "Photos and videos",
		"src.drive":    "Google Drive",
		"src.photos":   "Google Photos",
		"route":        "%s → %s",

		"photos.needGoogle": "Connect Google first.",
		"photos.why":        "Google no longer lets apps read photo libraries. You ask Google for the copy, we do the rest.",

		"btn.google":        "Connect Google",
		"btn.nextcloud":     "Connect Nextcloud",
		"btn.start":         "Start the copy",
		"btn.how":           "Show me how",
		"btn.asked":         "I have asked Google",
		"btn.stop":          "Stop",
		"btn.retry":         "Try again",
		"btn.upload":        "Send the file myself",
		"btn.takeout":       "Open Takeout",
		"btn.next":          "Next",
		"btn.choose":        "Choose a file",
		"btn.openNextcloud": "Open Nextcloud",
		"btn.openImmich":    "Open Immich",
		"btn.details":       "Details",
		"btn.saveKey":       "Save the key",

		"immich.how":   "In Immich: profile menu → User settings → API Keys → Create, tick Select all, then paste the key below.",
		"immich.open":  "Open Immich",
		"immich.field": "Immich API key",
		"immich.own":   "Your own key, so the import lands in your account.",

		"immich.connected": "Immich connected.",
		"immich.invalid":   "Immich did not accept that key.",
		"immich.empty":     "Paste a key first.",

		"unavailable":    "Not available. Ask whoever runs the server.",
		"hint.readOnly":  "Read-only.",
		"hint.nextcloud": "One confirmation, no password.",

		"pill.idle":        "Not started",
		"pill.connecting":  "Connecting",
		"pill.copying":     "Copying",
		"pill.importing":   "Importing",
		"pill.checking":    "Checking",
		"pill.waitGoogle":  "Waiting for Google",
		"pill.waitYou":     "Needs you",
		"pill.uploading":   "Uploading",
		"pill.downloading": "Downloading",
		"pill.done":        "Done",
		"pill.stopped":     "Stopped",

		"fact.files":  "%s files",
		"fact.photos": "%s photos",

		"wait.watching": "Watching for the folder “%s” in your Drive.",
		"wait.every":    "We look every %s.",
		"wait.horizon":  "A few hours, up to a day for a large library.",

		"takeout.title":   "Ask Google for a copy of your photos",
		"takeout.s1":      "Open Google Takeout",
		"takeout.s2":      "Choose Google Photos only",
		"takeout.s3":      "Set the export",
		"takeout.s4":      "Ask Google to start",
		"takeout.s5":      "Wait for the folder “%s” in your Drive",
		"takeout.where":   "Where it goes",
		"takeout.often":   "How often",
		"takeout.type":    "File type",
		"takeout.size":    "File size",
		"takeout.drive":   "Add to Drive",
		"takeout.once":    "Export once",
		"takeout.zip":     ".zip",
		"takeout.gb":      "50 GB",
		"takeout.wontFit": "Will not fit in your Google storage?",

		"upload.title":    "Send us the file",
		"upload.how":      "Download the export from Google, then add the files here.",
		"upload.drop":     "Drop a file here",
		"upload.keepOpen": "Keep this tab open while it sends.",
		"upload.resumes":  "If it stops, it continues from the last piece.",
		"upload.needsJs":  "Sending a file needs JavaScript.",
		// {file}, {sent} and {total} are filled in by the upload script.
		"upload.sending":  "Sending {file}",
		"upload.progress": "{sent} of {total} parts",
		"upload.sent":     "Sent. The import has started.",
		"upload.failed":   "Interrupted. Try again: it continues from the last piece.",

		"done.title":     "Done",
		"done.google":    "Nothing was removed from Google.",
		"done.plain":     "The copy and the import finished without errors.",
		"check.ok":       "Checked %s files against the originals. All matched.",
		"check.mismatch": "Checked %s files. %s did not match.",

		"error.resume":  "%s files are already in Nextcloud. Starting again continues from there.",
		"error.noFiles": "Nothing was copied yet.",

		"quota.over": "Over budget: %s of %s. The copy still runs.",

		"time.sec":  "s",
		"time.min":  "min",
		"time.hour": "h",
		"time.day":  "d",
	},

	IT: {
		"lang.name":   "Italiano",
		"lang.switch": "Lingua",

		"page.title":      "Porta i tuoi dati fuori da Google",
		"nothing.deleted": "Da Google non viene cancellato niente.",
		"tab.close":       "Il lavoro continua anche se chiudi questa scheda.",

		"drive.title":  "File e cartelle",
		"photos.title": "Foto e video",
		"src.drive":    "Google Drive",
		"src.photos":   "Google Foto",
		"route":        "%s → %s",

		"photos.needGoogle": "Prima collega Google.",
		"photos.why":        "Google non lascia più che un'app legga le librerie di foto. La copia la chiedi tu a Google, il resto lo facciamo noi.",

		"btn.google":        "Collega Google",
		"btn.nextcloud":     "Collega Nextcloud",
		"btn.start":         "Avvia la copia",
		"btn.how":           "Mostrami come",
		"btn.asked":         "Ho chiesto a Google",
		"btn.stop":          "Ferma",
		"btn.retry":         "Riprova",
		"btn.upload":        "Mando io il file",
		"btn.takeout":       "Apri Takeout",
		"btn.next":          "Avanti",
		"btn.choose":        "Scegli un file",
		"btn.openNextcloud": "Apri Nextcloud",
		"btn.openImmich":    "Apri Immich",
		"btn.details":       "Dettagli",
		"btn.saveKey":       "Salva la chiave",

		"immich.how":   "In Immich: menu profilo → Impostazioni utente → API Keys → Create, spunta Select all, poi incolla la chiave qui sotto.",
		"immich.open":  "Apri Immich",
		"immich.field": "Chiave API di Immich",
		"immich.own":   "È la tua chiave, così l'importazione finisce nel tuo account.",

		"immich.connected": "Immich collegato.",
		"immich.invalid":   "Immich non ha accettato quella chiave.",
		"immich.empty":     "Prima incolla una chiave.",

		"unavailable":    "Non disponibile. Chiedi a chi gestisce il server.",
		"hint.readOnly":  "Solo lettura.",
		"hint.nextcloud": "Una conferma, nessuna password.",

		"pill.idle":        "Non iniziato",
		"pill.connecting":  "Collegamento",
		"pill.copying":     "Copia",
		"pill.importing":   "Importazione",
		"pill.checking":    "Verifica",
		"pill.waitGoogle":  "In attesa di Google",
		"pill.waitYou":     "Serve te",
		"pill.uploading":   "Caricamento",
		"pill.downloading": "Scaricamento",
		"pill.done":        "Completato",
		"pill.stopped":     "Fermo",

		"fact.files":  "%s file",
		"fact.photos": "%s foto",

		"wait.watching": "Aspettiamo la cartella «%s» nel tuo Drive.",
		"wait.every":    "Guardiamo ogni %s.",
		"wait.horizon":  "Qualche ora, fino a un giorno per una libreria grande.",

		"takeout.title":   "Chiedi a Google una copia delle tue foto",
		"takeout.s1":      "Apri Google Takeout",
		"takeout.s2":      "Scegli solo Google Foto",
		"takeout.s3":      "Imposta l'esportazione",
		"takeout.s4":      "Chiedi a Google di iniziare",
		"takeout.s5":      "Aspetta la cartella «%s» nel tuo Drive",
		"takeout.where":   "Dove va",
		"takeout.often":   "Ogni quanto",
		"takeout.type":    "Tipo di file",
		"takeout.size":    "Dimensione file",
		"takeout.drive":   "Aggiungi a Drive",
		"takeout.once":    "Esporta una volta",
		"takeout.zip":     ".zip",
		"takeout.gb":      "50 GB",
		"takeout.wontFit": "Non ci sta nel tuo spazio Google?",

		"upload.title":    "Mandaci il file",
		"upload.how":      "Scarica l'esportazione da Google, poi aggiungi qui i file.",
		"upload.drop":     "Trascina qui un file",
		"upload.keepOpen": "Tieni aperta questa scheda mentre carica.",
		"upload.resumes":  "Se si ferma, riprende dall'ultimo pezzo.",
		"upload.needsJs":  "Per inviare un file serve JavaScript.",
		"upload.sending":  "Invio di {file}",
		"upload.progress": "{sent} parti su {total}",
		"upload.sent":     "Inviato. L'importazione è partita.",
		"upload.failed":   "Interrotto. Riprova: continua dall'ultimo pezzo.",

		"done.title":     "Fatto",
		"done.google":    "Da Google non è stato tolto niente.",
		"done.plain":     "La copia e l'importazione sono finite senza errori.",
		"check.ok":       "Controllati %s file con gli originali. Tutti uguali.",
		"check.mismatch": "Controllati %s file. %s non corrispondono.",

		"error.resume":  "%s file sono già in Nextcloud. Ripartendo si continua da lì.",
		"error.noFiles": "Non è stato ancora copiato niente.",

		"quota.over": "Oltre il budget: %s su %s. La copia parte lo stesso.",

		"time.sec":  "s",
		"time.min":  "min",
		"time.hour": "h",
		"time.day":  "g",
	},
}
