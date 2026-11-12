package syncrun

// LayerStepJSON is what one per-layer step came to at one layer, for --json.
type LayerStepJSON struct {
	Branch string
	Result string
	Owner  *string
	Commit *string
	Proof  *string
}
