package tui

import (
	"runtime/debug"
	"sync"
	"time"
)

var imageMemorySweep struct {
	sync.Mutex
	pending bool
}

// Release drops a closed preview's source and encoded raster.
func (im *TerminalImage) Release() {
	if im == nil || im.source == nil {
		return
	}

	im.source = nil
	im.sixelColumns = nil

	if im.width*im.height < 1<<20 && (!im.sixel || im.rasterWidth*im.rasterHeight < 256*1024) {
		return
	}

	imageMemorySweep.Lock()
	defer imageMemorySweep.Unlock()

	if imageMemorySweep.pending {
		return
	}

	imageMemorySweep.pending = true
	// Keep forced collection off the input path and coalesce rapidly closed previews.
	time.AfterFunc(750*time.Millisecond, func() {
		debug.FreeOSMemory()
		imageMemorySweep.Lock()
		imageMemorySweep.pending = false
		imageMemorySweep.Unlock()
	})
}
