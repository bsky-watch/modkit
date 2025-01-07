package reportqueue

const (
	valkeyStreamName           = "automod:reports"
	valkeyQuarantineStreamName = "automod:reports:quarantine"

	// This is annoying and should probably have tests to ensure
	// there are no unintended overlaps between various values.
	valkeyReportIdBits       = 48
	valkeyReportIdLocalBits  = 38
	valkeyReportIdNodeIdBits = valkeyReportIdBits - valkeyReportIdLocalBits
	valkeyReportIdRangeLen   = 1 << valkeyReportIdBits
	valkeyMaxNodeId          = (1<<valkeyReportIdNodeIdBits - 1)
	valkeyReportIdRangeStart = (1<<64 - valkeyReportIdRangeLen)
)
