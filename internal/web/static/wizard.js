// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Two jobs: poll /status so a copy that runs for hours is not a frozen screen,
// and drive the resumable Takeout upload. The page works without JavaScript
// too: it just does not update on its own and cannot upload, and a reload
// shows the current state.

const POLL_MS = 5000;

// True while the upload panel is sending files. A reload would cut the upload
// short, so nothing reloads meanwhile; the upload reloads itself when it ends.
// Read by render() below, set by start() inside wireUpload().
let busy = false;

function render(state) {
	// The upload screen is a panel, not a track card, so the per-track loop below
	// does not cover it. Handle it here: reload when the Photos state changes
	// (the import started, or stopped) so a different screen is shown, reload
	// when every declared part has arrived so the Start button appears, and
	// otherwise update the "X of Y here" lines in place as parts land. No reload
	// while an upload is running: it reloads by itself when it ends.
	const panel = document.getElementById('upload');
	if (panel) {
		const photos = (state.tracks ?? []).find((t) => t.Track === 'photos');
		if (!busy && photos && panel.dataset.state && panel.dataset.state !== photos.State) {
			location.reload();
			return;
		}
		if (state.parts) {
			// Reveal the Start button once every part is here — but only once:
			// reload only if it is not already on the page, or /status reporting
			// complete on every poll would reload in a loop.
			if (state.parts.complete && !busy && !panel.querySelector('[data-import-now]')) {
				location.reload();
				return;
			}
			const status = document.getElementById('parts-status');
			if (status) status.textContent = state.parts.status;
			const missing = document.getElementById('parts-missing');
			if (missing) {
				missing.textContent = state.parts.missing;
				missing.hidden = !state.parts.missing;
			}
		}
	}

	for (const track of state.tracks ?? []) {
		const card = document.querySelector(`[data-track="${track.Track}"]`);
		if (!card) continue;

		// A different state means a different screen: the server decides what
		// that looks like, so ask it rather than guessing here. Not mid-upload:
		// the Drive card shares the upload screen, and its state changing must
		// not cut the upload short.
		if (!busy && card.dataset.state && card.dataset.state !== track.State) {
			location.reload();
			return;
		}

		const pill = card.querySelector('.pill');
		if (pill && track.Pill) {
			pill.textContent = track.Pill;
			pill.className = `pill ${track.PillClass}`;
		}

		// Every string below was rendered by the server in the reader's
		// language. Nothing here formats a number or picks a word.
		set(card, '.facts', track.Facts);
		// A failed track's Progress holds the same reason as the notice below
		// it, which shows it untruncated. Writing it here too would put the
		// reason back on the card as a second, ellipsised copy on every poll,
		// even though the server deliberately left it out of the markup.
		set(card, '.now-doing', track.Failed ? '' : track.Progress);
	}
}

// set writes one line inside a card, creating it when there is something to
// say and removing it when there is not.
function set(card, selector, text) {
	const region = card.querySelector('.state');
	if (!region) return;
	let el = region.querySelector(selector);
	if (!text) {
		if (el) el.remove();
		return;
	}
	if (!el) {
		el = document.createElement('p');
		el.className = selector.slice(1);
		region.append(el);
	}
	el.textContent = text;
	if (selector === '.now-doing') el.title = text;
}

async function poll() {
	try {
		const res = await fetch('/status', {
			headers: { Accept: 'application/json' },
			credentials: 'same-origin',
		});
		if (!res.ok) return;
		render(await res.json());
	} catch {
		// A transient failure is not worth telling the person about: the next
		// poll will either work or the page reload will show the truth.
	}
}

setInterval(poll, POLL_MS);
poll();

// --- Resumable upload -------------------------------------------------------

// Minimal streaming SHA-256 (FIPS 180-4). Web Crypto's digest() is one-shot,
// so a 50 GB file would have to be held in memory; this hashes it in slices.
class Sha256 {
	constructor() {
		// Int32Array, like K and w below: a value past 2^31 in a Uint32Array or
		// a plain array reads back as a double, and the arithmetic slows down.
		this.h = new Int32Array([0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a,
		                         0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19]);
		this.buf = new Uint8Array(64);
		this.bufLen = 0;
		this.length = 0; // total bytes
	}

	static K = new Int32Array([
		0x428a2f98,0x71374491,0xb5c0fbcf,0xe9b5dba5,0x3956c25b,0x59f111f1,0x923f82a4,0xab1c5ed5,
		0xd807aa98,0x12835b01,0x243185be,0x550c7dc3,0x72be5d74,0x80deb1fe,0x9bdc06a7,0xc19bf174,
		0xe49b69c1,0xefbe4786,0x0fc19dc6,0x240ca1cc,0x2de92c6f,0x4a7484aa,0x5cb0a9dc,0x76f988da,
		0x983e5152,0xa831c66d,0xb00327c8,0xbf597fc7,0xc6e00bf3,0xd5a79147,0x06ca6351,0x14292967,
		0x27b70a85,0x2e1b2138,0x4d2c6dfc,0x53380d13,0x650a7354,0x766a0abb,0x81c2c92e,0x92722c85,
		0xa2bfe8a1,0xa81a664b,0xc24b8b70,0xc76c51a3,0xd192e819,0xd6990624,0xf40e3585,0x106aa070,
		0x19a4c116,0x1e376c08,0x2748774c,0x34b0bcb5,0x391c0cb3,0x4ed8aa4a,0x5b9cca4f,0x682e6ff3,
		0x748f82ee,0x78a5636f,0x84c87814,0x8cc70208,0x90befffa,0xa4506ceb,0xbef9a3f7,0xc67178f2,
	]);

	update(data) {
		this.length += data.length;
		let i = 0;
		if (this.bufLen > 0) {
			const need = 64 - this.bufLen;
			const take = Math.min(need, data.length);
			this.buf.set(data.subarray(0, take), this.bufLen);
			this.bufLen += take;
			i = take;
			if (this.bufLen === 64) {
				this.block(this.buf, 0);
				this.bufLen = 0;
			}
		}
		for (; i + 64 <= data.length; i += 64) this.block(data, i);
		if (i < data.length) {
			this.buf.set(data.subarray(i), 0);
			this.bufLen = data.length - i;
		}
	}

	// block is the hot loop: it runs once per 64 bytes, so it allocates nothing
	// and keeps the state in locals. Written the obvious way it hashes about
	// 10 MB/s, which is over an hour for one 50 GB Takeout part.
	block(bytes, offset) {
		const w = this.w ?? (this.w = new Int32Array(64));
		const k = Sha256.K;
		for (let i = 0; i < 16; i++) {
			const j = offset + i * 4;
			w[i] = (bytes[j] << 24) | (bytes[j+1] << 16) | (bytes[j+2] << 8) | bytes[j+3];
		}
		for (let i = 16; i < 64; i++) {
			const x = w[i-15], y = w[i-2];
			const s0 = ((x >>> 7) | (x << 25)) ^ ((x >>> 18) | (x << 14)) ^ (x >>> 3);
			const s1 = ((y >>> 17) | (y << 15)) ^ ((y >>> 19) | (y << 13)) ^ (y >>> 10);
			w[i] = (w[i-16] + s0 + w[i-7] + s1) | 0;
		}
		const hs = this.h;
		let a = hs[0], b = hs[1], c = hs[2], d = hs[3], e = hs[4], f = hs[5], g = hs[6], h = hs[7];
		for (let i = 0; i < 64; i++) {
			const S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7));
			const ch = (e & f) ^ (~e & g);
			const t1 = (h + S1 + ch + k[i] + w[i]) | 0;
			const S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10));
			const maj = (a & b) ^ (a & c) ^ (b & c);
			const t2 = (S0 + maj) | 0;
			h = g; g = f; f = e; e = (d + t1) | 0; d = c; c = b; b = a; a = (t1 + t2) | 0;
		}
		hs[0] += a; hs[1] += b; hs[2] += c; hs[3] += d;
		hs[4] += e; hs[5] += f; hs[6] += g; hs[7] += h;
	}

	hex() {
		const bitLenHi = Math.floor(this.length / 0x20000000);
		const bitLenLo = (this.length * 8) >>> 0;
		const pad = new Uint8Array(((this.bufLen < 56) ? 64 : 128) - this.bufLen);
		pad[0] = 0x80;
		const dv = new DataView(pad.buffer);
		dv.setUint32(pad.length - 8, bitLenHi);
		dv.setUint32(pad.length - 4, bitLenLo);
		this.update(pad);
		return [...this.h].map((x) => (x >>> 0).toString(16).padStart(8, '0')).join('');
	}
}


const CHUNK_SIZE = 16 * 1024 * 1024; // 16 MiB: comfortably under the server cap
const CHUNK_TIMEOUT_MS = 2 * 60 * 1000; // a stalled chunk is retried, not waited on
const MAX_RETRIES = 5;
const RETRY_BASE_MS = 1000;
const MAX_PASSES = 100; // a bound on the resume loop, so it can never spin forever

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

function fmt(bytes) {
	const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
	let n = bytes, i = 0;
	while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
	return `${n.toFixed(1)} ${units[i]}`;
}

async function api(path, options = {}) {
	const res = await fetch(path, {
		credentials: 'same-origin',
		headers: { Accept: 'application/json', ...(options.headers ?? {}) },
		...options,
	});
	if (!res.ok) {
		const text = await res.text();
		const err = new Error(text.trim() || `request failed (${res.status})`);
		err.status = res.status;
		throw err;
	}
	if (res.status === 204) return null;
	return res.json();
}

// upload sends the file in chunks. Before sending anything it asks the server
// what is already there and sends only that gap, so a reload, a dropped
// connection or a server restart resumes instead of starting over. The file's
// SHA-256 is announced first: it is what lets the server recognise the same
// content under a different name (Google renames a re-downloaded Takeout) and
// keep different content under a name already in use apart from it. The
// server answers with the name to use from then on, which is the existing
// upload's when the content is already known.
async function upload(file, onReading, onProgress) {
	const hash = await cachedHash(file, onReading);
	const session = await api('/upload/begin', {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ name: file.name, size: file.size, chunkSize: CHUNK_SIZE, hash }),
	});
	const name = session.name || file.name;

	if (session.complete) {
		onProgress(session.totalChunks, session.totalChunks);
		await api(`/upload/complete?name=${encodeURIComponent(name)}`, { method: 'POST' });
		return;
	}

	const total = session.totalChunks;
	let sent = total - (session.missing?.length ?? total);

	// The server decides what is missing, not the client. After a failure the
	// status is asked again, so a chunk that failed to land is retried while
	// everything that did land is skipped. This is the difference between a
	// resume and a restart.
	for (let pass = 0; pass < MAX_PASSES; pass++) {
		const state = pass === 0 ? session : await status(name);
		if (state.complete) break;

		const missing = state.missing ?? [];
		if (missing.length === 0) break;

		let progressed = false;
		for (const index of missing) {
			const start = index * CHUNK_SIZE;
			const end = Math.min(start + CHUNK_SIZE, file.size);
			await putChunk(name, index, file.slice(start, end));
			sent++;
			progressed = true;
			onProgress(sent, total);
		}
		if (!progressed) break;
	}

	try {
		await api(`/upload/complete?name=${encodeURIComponent(name)}`, { method: 'POST' });
	} catch (err) {
		// The server threw the chunks away. Forget the hash too, so the next
		// attempt reads the file again rather than trusting what may be wrong.
		if (err.status === 422) forgetHash(file);
		throw err;
	}
}

// status asks the server what still needs sending.
async function status(name) {
	return api(`/upload/status?name=${encodeURIComponent(name)}`);
}

// putChunk sends one chunk, retrying a dropped or hung request. Without a
// timeout a single stalled request freezes the whole upload with no message:
// the loop waits on a promise that may never settle, which is exactly the
// "looks stuck" the person sees.
async function putChunk(name, index, blob) {
	let lastError;
	for (let attempt = 0; attempt < MAX_RETRIES; attempt++) {
		if (attempt > 0) await sleep(RETRY_BASE_MS * 2 ** (attempt - 1));
		const controller = new AbortController();
		const timer = setTimeout(() => controller.abort(), CHUNK_TIMEOUT_MS);
		try {
			await api(`/upload/chunk?name=${encodeURIComponent(name)}&index=${index}`, {
				method: 'PUT',
				body: blob,
				signal: controller.signal,
			});
			return;
		} catch (err) {
			lastError = err;
		} finally {
			clearTimeout(timer);
		}
	}
	throw lastError ?? new Error('the chunk could not be sent');
}

// hashFile returns the lowercase hex SHA-256 of a File, read in slices so a
// 50 GB archive is never held in memory. crypto.subtle.digest is one-shot, so
// the slices are hashed by a streaming implementation instead: a one-shot
// digest would need the whole file in an ArrayBuffer. Reading 50 GB takes
// minutes, so it reports how far it got.
async function hashFile(file, onReading) {
	const CHUNK = 8 * 1024 * 1024;
	const hasher = new Sha256();
	for (let offset = 0; offset < file.size; offset += CHUNK) {
		const slice = file.slice(offset, Math.min(offset + CHUNK, file.size));
		hasher.update(new Uint8Array(await slice.arrayBuffer()));
		onReading(Math.floor(((offset + slice.size) / file.size) * 100));
	}
	return hasher.hex();
}

// cachedHash remembers a file's hash in this browser, so resuming after an
// interruption does not read the whole file again before sending the rest.
// The key includes the size and modification time: a different file under
// the same name is hashed afresh. Storage may be unavailable, and then the
// file is simply read again.
const hashKey = (file) => `zlatan.hash:${file.name}:${file.size}:${file.lastModified}`;

async function cachedHash(file, onReading) {
	try {
		const known = localStorage.getItem(hashKey(file));
		if (known) return known;
	} catch {}
	const hash = await hashFile(file, onReading);
	try {
		localStorage.setItem(hashKey(file), hash);
	} catch {}
	return hash;
}

function forgetHash(file) {
	try {
		localStorage.removeItem(hashKey(file));
	} catch {}
}

function wireUpload() {
	const root = document.getElementById('upload');
	if (!root) return;

	// Reveal what only works with a script running, and retire the note that
	// says so.
	for (const el of root.querySelectorAll('[data-upload-when-js]')) el.hidden = false;
	document.getElementById('upload-nojs')?.remove();

	const input = document.getElementById('upload-input');
	const progress = document.getElementById('upload-progress');
	const dropzone = document.getElementById('upload-drop');

	// Every phrase comes from the server, already translated. This fills in
	// the placeholders and nothing else.
	const say = (key, values = {}) =>
		Object.entries(values).reduce(
			(text, [k, v]) => text.replaceAll(`{${k}}`, v),
			root.dataset[key] ?? '');

	// The parts of one export go one after the other: each is resumable on its
	// own, and the server starts the import when the last declared part lands.
	// Then the page is reloaded, because what it shows (which parts are here,
	// or the import running) is the server's to say.
	// Files are queued, so dropping more parts while one is still uploading adds
	// them to the run instead of being silently discarded.
	const queue = [];
	const start = async (files) => {
		for (const f of files ?? []) queue.push(f);
		// Before the parts count is declared, the upload UI (and #upload-progress)
		// is not in the page yet: a drop then has nowhere to report, so ignore it.
		if (!progress || busy || queue.length === 0) return;
		busy = true;
		progress.hidden = false;
		let current = null;
		try {
			while (queue.length) {
				const file = queue.shift();
				current = file;
				progress.textContent = say('sending', { file: file.name });
				await upload(file, (percent) => {
					progress.textContent = say('reading', { file: file.name, percent });
				}, (sent, total) => {
					progress.textContent = `${file.name}: ${say('progress', { sent, total })}`;
				});
				progress.textContent = say('sent', { file: file.name });
			}
			location.reload();
		} catch (err) {
			// Name the file that failed, so on retry the person knows the parts
			// already sent are done.
			progress.textContent = say(err?.status === 422 ? 'mismatch' : 'failed', { file: current ? current.name : '' });
		} finally {
			busy = false;
		}
	};

	input?.addEventListener('change', () => start(input.files));

	// The auto-import checkbox saves on change without reloading, so toggling it
	// never interrupts an upload in progress. The Save button is for no-JS only.
	const autoForm = root.querySelector('[data-auto-form]');
	const autoToggle = root.querySelector('[data-auto-toggle]');
	if (autoForm && autoToggle) {
		root.querySelector('[data-auto-save]')?.setAttribute('hidden', '');
		autoToggle.addEventListener('change', () => {
			const body = new URLSearchParams();
			if (autoToggle.checked) body.set('auto', '1');
			fetch(autoForm.action, {
				method: 'POST',
				credentials: 'same-origin',
				headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
				body: body.toString(),
			}).then(() => {
				// Only turning it ON can start the import at once (all parts
				// already here): reload to show that. Turning it OFF changes
				// nothing to show, and never reload while an upload is running —
				// the reload would interrupt it, and no import can have started
				// mid-upload anyway.
				if (autoToggle.checked && !busy) location.reload();
			}).catch(() => {});
		});
	}

	// Drag and drop, with the zone lit only while a file is over it.
	root.addEventListener('dragover', (e) => {
		e.preventDefault();
		dropzone?.classList.add('route--over');
	});
	root.addEventListener('dragleave', () => dropzone?.classList.remove('route--over'));
	root.addEventListener('drop', (e) => {
		e.preventDefault();
		dropzone?.classList.remove('route--over');
		start(e.dataTransfer?.files);
	});
}

wireUpload();

// --- Open Immich in its app ------------------------------------------------
// The Android app registers the immich:// scheme, but verified app links exist
// only for my.immich.app, which needs the person to have saved their own
// server there first. So a self-hosted address cannot open the app by itself:
// try the scheme, and fall back to the web address the link already carries.
// With no script the link is just the web address, which is the right answer
// on a desktop anyway.
for (const link of document.querySelectorAll('[data-app-scheme]')) {
	link.addEventListener('click', (event) => {
		if (!matchMedia('(hover: none)').matches) return;
		event.preventDefault();

		const web = link.href;
		let left = false;
		const noteDeparture = () => { left = true; };
		document.addEventListener('visibilitychange', noteDeparture, { once: true });

		setTimeout(() => {
			document.removeEventListener('visibilitychange', noteDeparture);
			if (!left) location.href = web;
		}, 1000);

		location.href = link.dataset.appScheme;
	});
}
