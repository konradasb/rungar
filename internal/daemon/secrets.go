// Copyright 2026 Rungar Authors
// SPDX-License-Identifier: MIT

package daemon

import (
	"fmt"
	"log/slog"
	"os"
)

// warnExposed warns of each file that is world-readable, as ssh does of a
// private key. Files that cannot be stat'ed are left for loading to report.
func warnExposed(logger *slog.Logger, files []string) {
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}

		if mode := info.Mode().Perm(); mode&0o004 != 0 {
			logger.Warn("a file holding a secret is readable by anyone on the host; "+
				"make it readable by its owner and group alone",
				slog.String("file", f), slog.String("mode", fmt.Sprintf("%04o", mode)))
		}
	}
}
