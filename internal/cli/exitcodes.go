package cli

// SDK exit codes. Provider exits pass through unchanged; a signal n
// that terminates msgate itself maps to 128+n.
const (
	ExitOK                = 0
	ExitHomeMigration     = 1   // the old home could not be moved to the new one
	ExitUsage             = 2   // usage error detected before spawning
	ExitBusy              = 3   // conversation busy
	ExitNotFound          = 4   // run or conversation not found
	ExitWaitTimeout       = 5   // wait timed out
	ExitNotResumable      = 6   // conversation not resumable
	ExitPricesUnavailable = 7   // price list unavailable
	ExitInternal          = 70  // internal SDK error
	ExitTimeout           = 124 // provider timeout
	ExitLost              = 125 // job lost
	ExitProviderMissing   = 127 // provider binary not found
	ExitCancelled         = 130 // run cancelled by request
)
