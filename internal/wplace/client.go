package wplace

// Result 監視結果
type Result struct {
	DiffPixels     int
	DiffPercentage float64
	SnapshotURL    string
	LivePNG        []byte
}
