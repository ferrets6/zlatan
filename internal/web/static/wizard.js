// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Polls /status and updates the progress lines, so a copy that runs for hours
// is not a frozen screen. The page works without JavaScript too: it just does
// not update on its own, and a reload shows the current state.

const POLL_MS = 5000;

function render(state) {
	for (const track of state.tracks ?? []) {
		const el = document.querySelector(`.state[data-track="${track.Track}"]`);
		if (el) {
			el.textContent = track.Progress || el.textContent;
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
