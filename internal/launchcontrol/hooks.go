package launchcontrol

// Test seams for the two crash windows the log discipline exists to survive.
//
// ackHook runs after a request file is durable and before the caller is told
// so; a test that makes it fail models "the request is on disk but the agent
// never heard back". applyMarkerHook runs after a store commit and before the
// applied marker is written; a test that makes it fail models "the phase
// changed but the log does not know", which replay must recognize as case 1.
var (
	ackHook         = func() error { return nil }
	applyMarkerHook = func() error { return nil }
)
