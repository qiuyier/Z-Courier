package downlink

const (
	RetrySelectionModeFIFO = "fifo"
	RetrySelectionModeFair = "fair"

	DefaultRetryFairnessCandidateMultiplier = 4
	MaxRetryFairnessCandidateMultiplier     = 16
)

// RetryFairness controls bounded oversampling before due messages are selected
// round-robin by client and device. Disabled preserves the legacy FIFO order.
type RetryFairness struct {
	Enabled             bool
	CandidateMultiplier int
}

// CandidateLimit returns a bounded candidate window for one retry scan. A
// configured per-device pending limit expands the window far enough to see at
// least one message beyond a fully occupied device queue.
func (fairness RetryFairness) CandidateLimit(scanLimit, maxPendingPerDevice int) int {
	if scanLimit <= 0 {
		scanLimit = 100
	}
	multiplier := fairness.CandidateMultiplier
	if multiplier <= 0 {
		multiplier = DefaultRetryFairnessCandidateMultiplier
	}
	maxInt := int(^uint(0) >> 1)
	candidateLimit := maxInt
	if scanLimit <= maxInt/multiplier {
		candidateLimit = scanLimit * multiplier
	}
	if maxPendingPerDevice > 0 {
		capacityWindow := maxInt
		if maxPendingPerDevice <= maxInt-scanLimit {
			capacityWindow = maxPendingPerDevice + scanLimit
		}
		if capacityWindow > candidateLimit {
			candidateLimit = capacityWindow
		}
	}
	if candidateLimit < scanLimit {
		return scanLimit
	}
	return candidateLimit
}

// RetrySelection describes the bounded set chosen for one retry scan.
type RetrySelection struct {
	Messages     []Message
	Mode         string
	DeviceCount  int
	MaxPerDevice int
}

type retryDeviceKey struct {
	clientID string
	deviceID string
}

func fairRetrySelection(candidates []Message, limit int) RetrySelection {
	if limit <= 0 {
		limit = 100
	}
	positions := make(map[retryDeviceKey]int)
	rounds := make([][]int, 0, min(limit, len(candidates)))
	for index, message := range candidates {
		key := retryDeviceKey{clientID: message.ClientID, deviceID: message.DeviceID}
		position := positions[key]
		positions[key] = position + 1
		if position >= limit {
			continue
		}
		for len(rounds) <= position {
			rounds = append(rounds, nil)
		}
		rounds[position] = append(rounds[position], index)
	}

	selected := make([]Message, 0, min(limit, len(candidates)))
	for _, round := range rounds {
		for _, index := range round {
			selected = append(selected, candidates[index])
			if len(selected) == limit {
				break
			}
		}
		if len(selected) == limit {
			break
		}
	}
	return retrySelectionFromMessages(selected, RetrySelectionModeFair)
}

func retrySelectionFromMessages(messages []Message, mode string) RetrySelection {
	counts := make(map[retryDeviceKey]int)
	maxPerDevice := 0
	for _, message := range messages {
		key := retryDeviceKey{clientID: message.ClientID, deviceID: message.DeviceID}
		counts[key]++
		if counts[key] > maxPerDevice {
			maxPerDevice = counts[key]
		}
	}
	return RetrySelection{
		Messages:     messages,
		Mode:         mode,
		DeviceCount:  len(counts),
		MaxPerDevice: maxPerDevice,
	}
}

func normalizeRetrySelectionLimits(limit, candidateLimit int) (int, int) {
	if limit <= 0 {
		limit = 100
	}
	if candidateLimit < limit {
		candidateLimit = limit
	}
	return limit, candidateLimit
}
