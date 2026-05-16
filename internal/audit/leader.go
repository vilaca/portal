// Leader-election helpers separated from controller.go to keep the file
// focused on the EventSource path. The actual lifecycle (creating the
// resource lock, running leaderelection.RunOrDie, flipping the isLeader
// atomic) lives in controller.go; this file exists so external packages
// can introspect leadership state for tests.

package audit

// IsLeader reports whether this controller currently holds the lease. In
// LeaderElection=false mode this returns true once Start has been called.
func (c *Controller) IsLeader() bool { return c.isLeader.Load() }
