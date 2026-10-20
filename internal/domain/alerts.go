package domain

import "context"

// AlertCounter counts a workspace's open alerts (or every workspace's, for
// all = true). The trust layer raises the alerts; the analytics overview
// shows the count.
type AlertCounter interface {
	CountOpenAlerts(ctx context.Context, workspaceID int64, all bool) (int, error)
}
