# migrate

Servizio di **migrazione autoguidata** da Google: `migrate.feretti.link`.
Un utente entra, fa login col suo account homelab, e un wizard lo porta a
spostare **Google Drive → Nextcloud** e **Google Photos → Immich** senza
vedere un terminale. Un backend fa girare la migrazione in automatico; chi
gestisce l'homelab non è nel loop per ogni utente.

> **Stato: prima fase implementata.** Il servizio gira, lo schema c'è, il
> wizard rende e protegge l'accesso. **Non ancora fatto**: il collegamento
> OAuth Google, il runner che invoca `rclone` e `immich-go`, e l'upload del
> Takeout. Vedi "Cosa manca".

## Il vincolo che definisce il servizio

**Drive è automatizzabile al 100%; le Foto no.** Dal 2025-03-31 Google ha
rimosso le read scope della Photos Library API, e non esiste un'API per
avviare un Takeout (`takeout.google.com` non è programmabile; la Data
Portability API copre solo Chrome/Maps/Play/Search/Shopping/YouTube). Quindi
il percorso Foto ha sempre **un click umano**: il wizard lo guida, si accorge
da solo quando l'esportazione è pronta e fa tutto il resto. Non è un limite
superabile con più codice — è Google.

## Come è fatto

Un solo binario Go (`migrate`), dentro un container con `rclone` e `immich-go`.
SQLite per lo stato: una migrazione dura ore o giorni, non può vivere in una
richiesta HTTP.

```
internal/
├── core/       tipi di dominio, cifratura dei token (AES-GCM)
├── config/     configurazione da ambiente, con tutti gli errori in una passata
├── store/      SQLite, migrazioni, repository
└── web/        il wizard: rotte, autenticazione, template
```

Due binari indipendenti, `drive` e `photos`: si può migrare uno, l'altro, o
entrambi in parallelo. Lo stato di uno non tocca l'altro.

## Sicurezza

Il servizio **custodisce i refresh token OAuth Google di ogni utente**: ogni
token dà lettura dell'intero Drive di quella persona. È il segreto più
sensibile dell'homelab.

- **Token cifrati a riposo** (AES-GCM). La chiave (`MIGRATE_TOKEN_KEY`) vive
  solo nell'ambiente; il database contiene solo ciphertext.
- **L'identità viene solo dall'header forward-auth** (`Remote-User`), ed è
  creduta solo se la richiesta arriva dalla rete proxy configurata. Mai da un
  parametro URL o da un cookie: una persona può vedere solo la propria
  migrazione, per costruzione.
- **Segreto condiviso con il proxy** (`X-Migrate-Proxy-Secret`): un container
  sulla stessa rete Docker non può raggiungere il servizio direttamente e
  saltare l'SSO.
- **Un bind pubblico senza segreto è rifiutato all'avvio**, non accettato in
  silenzio.

## Configurazione

Vedi `.env.example`. Le variabili obbligatorie sono `MIGRATE_TRUSTED_PROXY`,
`MIGRATE_TOKEN_KEY` e — se il bind è pubblico — `MIGRATE_PROXY_SECRET`.

## Comandi

```bash
migrate serve      # migra lo schema e ascolta
migrate migrate    # applica le migrazioni ed esce
migrate version
```

## Sviluppo

```bash
make check    # gofmt, vet, test (con race detector)
make image    # costruisce l'immagine
```

## Cosa manca

- [ ] Flusso OAuth Google per-utente (`drive.readonly`), token sigillati nello
      store.
- [ ] `Runner` che invoca `rclone` per Drive → Nextcloud, con progresso.
- [ ] `Runner` che invoca `immich-go` per il Takeout → Immich.
- [ ] Upload resumibile del Takeout (variante B).
- [ ] Polling della cartella condivisa (variante A).
- [ ] Verifica (conteggio + campione) e purge dello staging.
- [ ] Quote: leggere l'utilizzo prima e avvisare se si sfora.

Design completo e decisioni in `../homelab/docs/migrate-service.md`.
