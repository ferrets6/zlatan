// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import "context"

// Noop is a Notifier that does nothing. It is what the runner uses when no
// ntfy is configured, so the code path is always present and a missing
// notification server never becomes a missing branch.
type Noop struct{}

// Notify does nothing and succeeds.
func (Noop) Notify(context.Context, Message) error { return nil }
