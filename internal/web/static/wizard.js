// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Two jobs: poll /status so a copy that runs for hours is not a frozen screen,
// and drive the resumable Takeout upload. The page works without JavaScript
// too: it just does not update on its own and cannot upload, and a reload
// shows the current state.

const POLL_MS = 5000;

function render(state) {
	for (const track of state.tracks ?? []) {
		const card = document.querySelector(`[data-track="${track.Track}"]`);
		if (!card) continue;

		// A different state means a different screen: the server decides what
		// that looks like, so ask it rather than guessing here.
		if (card.dataset.state && card.dataset.state !== track.State) {
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
		set(card, '.now-doing', track.Progress);
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

const CHUNK_SIZE = 16 * 1024 * 1024; // 16 MiB: comfortably under the server cap

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
		throw new Error(text.trim() || `request failed (${res.status})`);
	}
	if (res.status === 204) return null;
	return res.json();
}

// upload sends the file in chunks, asking the server what is still missing
// before each pass. An interrupted upload therefore resumes rather than
// restarting, which for a 50 GB archive is the difference between usable and
// not.
async function upload(file, onProgress) {
	const session = await api('/upload/begin', {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ name: file.name, size: file.size, chunkSize: CHUNK_SIZE }),
	});

	let sent = session.receivedChunks ?? 0;
	const total = session.totalChunks;

	for (let index = 0; index < total; index++) {
		const start = index * CHUNK_SIZE;
		const end = Math.min(start + CHUNK_SIZE, file.size);
		// skipDuplicates is false so a resume re-sends what the server says is
		// missing; a chunk already present is overwritten harmlessly.
		const blob = file.slice(start, end);

		await api(`/upload/chunk?name=${encodeURIComponent(file.name)}&index=${index}`, {
			method: 'PUT',
			body: blob,
		});

		sent = index + 1;
		onProgress(sent, total);
	}

	await api(`/upload/complete?name=${encodeURIComponent(file.name)}`, { method: 'POST' });
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

	const start = async (file) => {
		if (!file) return;
		progress.hidden = false;
		progress.textContent = say('sending', { file: file.name });
		try {
			await upload(file, (sent, total) => {
				progress.textContent = say('progress', { sent, total });
			});
			progress.textContent = say('sent');
			poll();
		} catch {
			progress.textContent = say('failed');
		}
	};

	input?.addEventListener('change', () => start(input.files?.[0]));

	// Drag and drop, with the zone lit only while a file is over it.
	root.addEventListener('dragover', (e) => {
		e.preventDefault();
		dropzone?.classList.add('drop--over');
	});
	root.addEventListener('dragleave', () => dropzone?.classList.remove('drop--over'));
	root.addEventListener('drop', (e) => {
		e.preventDefault();
		dropzone?.classList.remove('drop--over');
		start(e.dataTransfer?.files?.[0]);
	});
}

wireUpload();
