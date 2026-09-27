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

		"legal.privacy": "Privacy policy",
		"legal.terms":   "Terms of service",
		"legal.updated": "Last updated %s",
		"legal.back":    "Back to zlatan",
		"legal.source":  "Source code",

		"landing.what":    "zlatan moves one household's own data out of Google: files from Drive into Nextcloud, photos and videos from Google Photos into Immich.",
		"landing.private": "It runs on a home server and serves that household only. There is no public sign up, and no account can be created here.",
		"landing.signin":  "Members of the household reach it through the household sign on.",
		"landing.enter":   "Sign in",

		"page.title":      "Move your things off Google",
		"nothing.deleted": "Nothing is deleted from Google.",
		"tab.close":       "The work continues if you close this tab.",

		"drive.title": "Files and folders",
		// What rclone copy of "gdrive:" leaves out: files owned by others,
		// shared drives (a separate remote), and the types Google cannot export.
		"drive.notCopied": "Not copied: files others shared with you, shared drives, and Google Forms, Sites and My Maps, which Google does not export.",
		"photos.title":    "Photos and videos",
		"src.drive":       "Google Drive",
		"src.photos":      "Google Photos",
		"route":           "%s → %s",

		"photos.s1":        "Connect Immich",
		"photos.s2":        "Connect Google",
		"photos.s3":        "Ask Google for a copy",
		"photos.googleWhy": "So we can collect the export from your Drive.",

		"btn.google":        "Connect Google",
		"btn.nextcloud":     "Connect Nextcloud",
		"btn.start":         "Start the copy",
		"btn.asked":         "I have asked Google",
		"btn.stop":          "Stop",
		"btn.retry":         "Try again",
		"btn.retryImport":   "Import the file again",
		"btn.upload":        "Send the file myself",
		"btn.choose":        "Choose a file",
		"btn.openNextcloud": "Open Nextcloud",
		"btn.openImmich":    "Open Immich",
		"immich.inApp":      "Opens the app if it is installed.",
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

		"fact.files":  "%s",
		"fact.photos": "%s",

		// The noun of a count, in both numbers. English inflects; Italian does
		// not for these two nouns. Counts always travel through i18n.Files /
		// i18n.Photos, never as a bare number next to a noun, so "1 files"
		// cannot happen.
		"noun.file":   "file",
		"noun.files":  "files",
		"noun.photo":  "photo",
		"noun.photos": "photos",

		"wait.watching": "Watching for the folder “%s” in your Drive.",
		"wait.every":    "We look every %s.",
		"wait.horizon":  "A few hours, up to a day for a large library.",

		"takeout.s1":         "Open Google Takeout",
		"takeout.s2":         "Choose Google Photos only",
		"takeout.s3":         "Set the export",
		"takeout.s4":         "Ask Google to start",
		"takeout.where":      "Where it goes",
		"takeout.often":      "How often",
		"takeout.type":       "File type",
		"takeout.size":       "File size",
		"takeout.drive":      "Add to Drive",
		"takeout.once":       "Export once",
		"takeout.zip":        ".zip",
		"takeout.gb":         "50 GB",
		"takeout.wontFit":    "Will not fit in your Google storage?",
		"takeout.wontFitWhy": "Your Google storage does not have room for the export, so it cannot be put in your Drive. Send us the file instead: the button is below.",

		"upload.title":    "Send us the file",
		"upload.how":      "Download the export from Google, then add the files here.",
		"upload.drop":     "Drop a file here",
		"upload.keepOpen": "Keep this tab open while it sends.",
		"upload.resumes":  "If it stops, it continues from the last piece.",
		"upload.needsJs":  "Sending a file needs JavaScript.",
		// {file}, {percent}, {sent} and {total} are filled in by the upload
		// script.
		"upload.reading":  "Reading {file}: {percent}%",
		"upload.sending":  "Sending {file}",
		"upload.progress": "{sent} of {total} parts",
		"upload.sent":     "Sent. The import has started.",
		"upload.failed":   "Interrupted. Try again: it continues from the last piece.",
		"upload.mismatch": "The file did not arrive intact. Send it again: it starts from the beginning.",

		"done.title":          "Done",
		"done.google":         "Nothing was removed from Google.",
		"done.plain":          "The copy and the import finished without errors.",
		"done.takeoutCleanup": "If the export went to your Drive, delete the folder “%s” there: it still takes up your Google storage.",
		"check.ok":            "The check compared %s against the originals. Nothing differed.",
		"check.mismatch":      "The check compared %s. Differences: %s.",

		"error.resume":  "Already in Nextcloud: %s. Starting again continues from there.",
		"error.noFiles": "Nothing was copied yet.",

		// Progress lines, shown on the card as "now doing". The runner stores
		// only the key, and the numbers travel as arguments, so the sentence is
		// chosen here in the reader's language and the decimal mark follows it.
		"progress.preparing":       "Preparing the copy",
		"progress.copying":         "Copied %s in %s",
		"progress.checking":        "Checking the copy against your Drive",
		"progress.interrupted":     "Interrupted by a restart: it will pick up where it left off",
		"progress.nextcloudReady":  "Nextcloud is connected: ready to copy",
		"progress.googleReady":     "Google is connected: ready for the copy",
		"progress.consentPending":  "Waiting for your Nextcloud consent",
		"progress.awaitingUpload":  "Waiting for you to send the export",
		"progress.awaitingTakeout": "Waiting for Google to put the export in your Drive",
		"progress.downloading":     "Downloading the export from your Drive",
		"progress.importing":       "Importing into Immich",
		"progress.driveVerified":   "The copy finished and was checked",
		"progress.photosDone":      "The import into Immich finished",

		// Why a track went back to reconnect. These are reasons, so the same key
		// fills the progress line and the stopped screen's reason.
		"reconnect.googleCopy":    "Google is not connected: connect it before copying.",
		"reconnect.nextcloudCopy": "Nextcloud is not connected: connect it before copying.",
		"reconnect.bothCopy":      "Google and Nextcloud are not connected: connect them before copying.",
		"reconnect.googleExport":  "Google is not connected: connect it before asking for the export.",
		"reconnect.immichImport":  "Immich is not connected: add your API key before importing.",
		"reconnect.immichExport":  "Immich is not connected: add your API key before asking for the export.",

		// Failure reasons, shown on the stopped screen. The runner stores the
		// key, so the reason a migration stopped is rendered in the reader's
		// own language rather than the one the runner happens to speak.
		"why.nextcloudMissing":     "Nextcloud is not configured.",
		"why.copyPrepare":          "The copy could not be prepared.",
		"why.copyUnfinished":       "The copy from Google Drive did not finish.",
		"why.copyUnchecked":        "The copy finished but could not be checked.",
		"why.mismatch":             "The check found %s that did not match.",
		"why.takeoutLate":          "The export did not arrive in time: send the Takeout file instead.",
		"why.stagingPrepare":       "The staging area could not be prepared.",
		"why.stagingRead":          "The staging area could not be read.",
		"why.downloadFailed":       "The export could not be downloaded from your Drive.",
		"why.noArchive":            "No Takeout archive found: send one first.",
		"why.archiveEmpty":         "The archive did not contain any photos or videos to import.",
		"why.archiveUnreadable":    "The archive could not be read.",
		"why.immichMissing":        "Immich is not connected: add your API key before importing.",
		"why.importUnfinished":     "The import into Immich did not finish.",
		"why.importErrors":         "The import finished with %s errors and %s assets pending.",
		"why.credentialUnreadable": "A saved connection could not be read. Ask whoever runs the server.",

		"quota.over": "Over budget: %s of %s. The copy still runs.",

		// How much space the data takes up at Google, shown so the size of what
		// is being moved is visible. Drive is exact; Photos is an upper bound,
		// because Google reports it only inside "other" (Gmail plus Photos) and
		// the line says so rather than claiming a figure it cannot back.
		"space.drive":  "Google Drive: %s",
		"space.photos": "Google Photos: up to %s, Gmail included",

		"time.sec":  "s",
		"time.min":  "min",
		"time.hour": "h",
		"time.day":  "d",
	},

	IT: {
		"lang.name":   "Italiano",
		"lang.switch": "Lingua",

		"legal.privacy": "Informativa sulla privacy",
		"legal.terms":   "Termini di servizio",
		"legal.updated": "Ultimo aggiornamento %s",
		"legal.back":    "Torna a zlatan",
		"legal.source":  "Codice sorgente",

		"landing.what":    "zlatan sposta i dati di una famiglia fuori da Google: i file da Drive a Nextcloud, le foto e i video da Google Foto a Immich.",
		"landing.private": "Gira su un server di casa e serve solo quella famiglia. Non esiste una registrazione pubblica e qui non si può creare un account.",
		"landing.signin":  "Chi fa parte della famiglia entra con l'accesso unico di casa.",
		"landing.enter":   "Entra",

		"page.title":      "Porta i tuoi dati fuori da Google",
		"nothing.deleted": "Da Google non viene cancellato niente.",
		"tab.close":       "Il lavoro continua anche se chiudi questa scheda.",

		"drive.title":     "File e cartelle",
		"drive.notCopied": "Non vengono copiati: i file che altri hanno condiviso con te, i Drive condivisi, e Moduli, Sites e My Maps di Google, che Google non esporta.",
		"photos.title":    "Foto e video",
		"src.drive":       "Google Drive",
		"src.photos":      "Google Foto",
		"route":           "%s → %s",

		"photos.s1":        "Collega Immich",
		"photos.s2":        "Collega Google",
		"photos.s3":        "Chiedi a Google una copia",
		"photos.googleWhy": "Ci serve per prendere l'esportazione dal tuo Drive.",

		"btn.google":        "Collega Google",
		"btn.nextcloud":     "Collega Nextcloud",
		"btn.start":         "Avvia la copia",
		"btn.asked":         "Ho chiesto a Google",
		"btn.stop":          "Ferma",
		"btn.retry":         "Riprova",
		"btn.retryImport":   "Riprova l'importazione",
		"btn.upload":        "Mando io il file",
		"btn.choose":        "Scegli un file",
		"btn.openNextcloud": "Apri Nextcloud",
		"btn.openImmich":    "Apri Immich",
		"immich.inApp":      "Apre l'app se è installata.",
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

		"fact.files":  "%s",
		"fact.photos": "%s",

		"noun.file":   "file",
		"noun.files":  "file",
		"noun.photo":  "foto",
		"noun.photos": "foto",

		"wait.watching": "Aspettiamo la cartella «%s» nel tuo Drive.",
		"wait.every":    "Guardiamo ogni %s.",
		"wait.horizon":  "Qualche ora, fino a un giorno per una libreria grande.",

		"takeout.s1":         "Apri Google Takeout",
		"takeout.s2":         "Scegli solo Google Foto",
		"takeout.s3":         "Imposta l'esportazione",
		"takeout.s4":         "Chiedi a Google di iniziare",
		"takeout.where":      "Dove va",
		"takeout.often":      "Ogni quanto",
		"takeout.type":       "Tipo di file",
		"takeout.size":       "Dimensione file",
		"takeout.drive":      "Aggiungi a Drive",
		"takeout.once":       "Esporta una volta",
		"takeout.zip":        ".zip",
		"takeout.gb":         "50 GB",
		"takeout.wontFit":    "Non ci sta nel tuo spazio Google?",
		"takeout.wontFitWhy": "Nel tuo spazio Google non c'è posto per l'esportazione, quindi non può essere messa nel tuo Drive. Mandaci tu il file: il pulsante è qui sotto.",

		"upload.title":    "Mandaci il file",
		"upload.how":      "Scarica l'esportazione da Google, poi aggiungi qui i file.",
		"upload.drop":     "Trascina qui un file",
		"upload.keepOpen": "Tieni aperta questa scheda mentre carica.",
		"upload.resumes":  "Se si ferma, riprende dall'ultimo pezzo.",
		"upload.needsJs":  "Per inviare un file serve JavaScript.",
		"upload.reading":  "Lettura di {file}: {percent}%",
		"upload.sending":  "Invio di {file}",
		"upload.progress": "{sent} parti su {total}",
		"upload.sent":     "Inviato. L'importazione è partita.",
		"upload.failed":   "Interrotto. Riprova: continua dall'ultimo pezzo.",
		"upload.mismatch": "Il file non è arrivato integro. Mandalo di nuovo: riparte dall'inizio.",

		"done.title":          "Fatto",
		"done.google":         "Da Google non è stato tolto niente.",
		"done.plain":          "La copia e l'importazione sono finite senza errori.",
		"done.takeoutCleanup": "Se l'esportazione è finita nel tuo Drive, elimina lì la cartella “%s”: occupa ancora il tuo spazio Google.",
		"check.ok":            "Il controllo ha confrontato %s con gli originali. Nessuna differenza.",
		"check.mismatch":      "Il controllo ha confrontato %s. Differenze: %s.",

		"error.resume":  "Già in Nextcloud: %s. Ripartendo si continua da lì.",
		"error.noFiles": "Non è stato ancora copiato niente.",

		"progress.preparing":       "Preparo la copia",
		"progress.copying":         "Copiati %s in %s",
		"progress.checking":        "Controllo la copia con il tuo Drive",
		"progress.interrupted":     "Interrotta da un riavvio: riprende da dove era rimasta",
		"progress.nextcloudReady":  "Nextcloud è collegato: pronti a copiare",
		"progress.googleReady":     "Google è collegato: pronti a copiare",
		"progress.consentPending":  "In attesa del tuo consenso a Nextcloud",
		"progress.awaitingUpload":  "In attesa che tu mandi l'esportazione",
		"progress.awaitingTakeout": "In attesa che Google metta l'esportazione nel tuo Drive",
		"progress.downloading":     "Scarico l'esportazione dal tuo Drive",
		"progress.importing":       "Importo in Immich",
		"progress.driveVerified":   "La copia è finita ed è stata controllata",
		"progress.photosDone":      "L'importazione in Immich è finita",

		"reconnect.googleCopy":    "Google non è collegato: collegalo prima di copiare.",
		"reconnect.nextcloudCopy": "Nextcloud non è collegato: collegalo prima di copiare.",
		"reconnect.bothCopy":      "Google e Nextcloud non sono collegati: collegali prima di copiare.",
		"reconnect.googleExport":  "Google non è collegato: collegalo prima di chiedere l'esportazione.",
		"reconnect.immichImport":  "Immich non è collegato: aggiungi la tua API key prima di importare.",
		"reconnect.immichExport":  "Immich non è collegato: aggiungi la tua API key prima di chiedere l'esportazione.",

		"why.nextcloudMissing":     "Nextcloud non è configurato.",
		"why.copyPrepare":          "La copia non è potuta partire.",
		"why.copyUnfinished":       "La copia da Google Drive non è finita.",
		"why.copyUnchecked":        "La copia è finita ma non è stato possibile controllarla.",
		"why.mismatch":             "Il controllo ha trovato %s che non corrispondono.",
		"why.takeoutLate":          "L'esportazione non è arrivata in tempo: manda il file del Takeout.",
		"why.stagingPrepare":       "Non è stato possibile preparare l'area di lavoro.",
		"why.stagingRead":          "Non è stato possibile leggere l'area di lavoro.",
		"why.downloadFailed":       "Non è stato possibile scaricare l'esportazione dal tuo Drive.",
		"why.noArchive":            "Nessun archivio Takeout trovato: mandane uno prima.",
		"why.archiveEmpty":         "L'archivio non conteneva foto o video da importare.",
		"why.archiveUnreadable":    "Non è stato possibile leggere l'archivio.",
		"why.immichMissing":        "Immich non è collegato: aggiungi la tua API key prima di importare.",
		"why.importUnfinished":     "L'importazione in Immich non è finita.",
		"why.importErrors":         "L'importazione è finita con %s errori e %s elementi in sospeso.",
		"why.credentialUnreadable": "Non è stato possibile leggere un collegamento salvato. Chiedi a chi gestisce il server.",

		"quota.over": "Oltre il budget: %s su %s. La copia parte lo stesso.",

		"space.drive":  "Google Drive: %s",
		"space.photos": "Google Foto: fino a %s, Gmail incluso",

		"time.sec":  "s",
		"time.min":  "min",
		"time.hour": "h",
		"time.day":  "g",
	},
}
