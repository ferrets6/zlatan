// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Two jobs: poll /status so a copy that runs for hours is not a frozen screen,
// and drive the resumable Takeout upload. The page works without JavaScript
// too: it just does not update on its own and cannot upload, and a reload
// shows the current state.

const POLL_MS = 5000;

function render(state) {
	for (const track of state.tracks ?? []) {
		// The live region is the whole .state block, so the calm line and the
		// technical detail underneath are always a matching pair.
		const root = document.querySelector(`[data-track="${track.Track}"]`)?.closest('.state');
		if (!root) continue;

		const line = root.querySelector('.state__line');
		if (line && track.Line) line.textContent = track.Line;

		let now = root.querySelector('.now-doing');
		if (track.Progress) {
			if (!now) {
				now = document.createElement('p');
				now.className = 'now-doing';
				root.append(now);
			}
			now.textContent = track.Progress;
			now.title = track.Progress;
		} else if (now) {
			now.remove();
		}
	}
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

	// The controls are hidden in the markup so a browser without JavaScript
	// sees only the noscript note, not a button that cannot work. This is the
	// one place that turns them on.
	for (const el of root.querySelectorAll('[data-upload-when-js]')) el.hidden = false;

	const input = document.getElementById('upload-input');
	const button = document.getElementById('upload-button');
	const progress = document.getElementById('upload-progress');
	const dropzone = document.getElementById('upload-drop');

	const start = async (file) => {
		if (!file) return;
		progress.hidden = false;
		progress.textContent = `Preparing to upload ${file.name}…`;
		button.disabled = true;
		try {
			await upload(file, (sent, total) => {
				const pct = Math.round((sent / total) * 100);
				progress.textContent = `Sent ${sent} of ${total} parts (${pct}%) — keep this tab open while it sends.`;
			});
			progress.textContent = 'Upload complete: the import has started. You can close the page now.';
			poll();
		} catch (err) {
			progress.textContent = `Upload interrupted: ${err.message}. Try again: it resumes where it left off.`;
		} finally {
			button.disabled = false;
		}
	};

	button?.addEventListener('click', () => input?.click());
	input?.addEventListener('change', () => start(input.files?.[0]));

	// Drag and drop, with the highlight only while a file is over the zone.
	root.addEventListener('dragover', (e) => {
		e.preventDefault();
		if (dropzone) dropzone.hidden = false;
	});
	root.addEventListener('dragleave', () => {
		if (dropzone) dropzone.hidden = true;
	});
	root.addEventListener('drop', (e) => {
		e.preventDefault();
		if (dropzone) dropzone.hidden = true;
		start(e.dataTransfer?.files?.[0]);
	});
}

wireUpload();

// --- Copy to clipboard ------------------------------------------------------
// Progressive enhancement, and nothing more: without this the address is still
// on the page and still selectable.
for (const btn of document.querySelectorAll('[data-copy]')) {
	btn.addEventListener('click', () => {
		const text = document.querySelector(btn.dataset.copy)?.textContent.trim();
		if (!text) return;
		navigator.clipboard.writeText(text).then(() => {
			const was = btn.textContent;
			btn.textContent = 'Copied';
			setTimeout(() => { btn.textContent = was; }, 2000);
		});
	});
}
