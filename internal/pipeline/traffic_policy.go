package pipeline

import "sort"

type TrafficPolicySelection struct {
	Name        string
	Key         string
	TokenBucket TokenBucketConfig
}

type TrafficPolicySelector struct {
	policies      []compiledTrafficPolicy
	routes        []TrafficPolicyRoute
	defaultPolicy TrafficPolicySelection
	hasDefault    bool
}

type compiledTrafficPolicy struct {
	selection TrafficPolicySelection
	priority  int
	match     TrafficPolicyMatch
	clientIDs map[string]struct{}
	routes    map[string]struct{}
}

func NewTrafficPolicySelector(config TrafficPoliciesConfig) *TrafficPolicySelector {
	selector := &TrafficPolicySelector{
		policies: make([]compiledTrafficPolicy, 0, len(config.Policies)),
		routes:   append([]TrafficPolicyRoute(nil), config.Routes...),
	}
	for _, policy := range config.Policies {
		compiled := compiledTrafficPolicy{
			selection: TrafficPolicySelection{
				Name:        policy.Name,
				Key:         policy.Key,
				TokenBucket: policy.TokenBucket,
			},
			priority:  policy.Priority,
			match:     cloneTrafficPolicyMatch(policy.Match),
			clientIDs: makeStringSet(policy.Match.ClientIDs),
			routes:    makeStringSet(policy.Match.Routes),
		}
		selector.policies = append(selector.policies, compiled)
	}

	sort.Slice(selector.policies, func(left, right int) bool {
		if selector.policies[left].priority != selector.policies[right].priority {
			return selector.policies[left].priority > selector.policies[right].priority
		}
		return selector.policies[left].selection.Name < selector.policies[right].selection.Name
	})
	for _, policy := range selector.policies {
		if policy.selection.Name == config.DefaultPolicy {
			selector.defaultPolicy = policy.selection
			selector.hasDefault = true
			break
		}
	}

	return selector
}

func (s *TrafficPolicySelector) Select(clientID string, msgID uint32) (TrafficPolicySelection, bool) {
	if s == nil {
		return TrafficPolicySelection{}, false
	}

	routeName, routeFound := s.resolveRoute(msgID)
	return s.SelectResolved(clientID, msgID, routeName, routeFound)
}

func (s *TrafficPolicySelector) SelectResolved(
	clientID string,
	msgID uint32,
	routeName string,
	routeFound bool,
) (TrafficPolicySelection, bool) {
	if s == nil {
		return TrafficPolicySelection{}, false
	}

	for _, policy := range s.policies {
		if policy.matches(clientID, msgID, routeName, routeFound) {
			return policy.selection, true
		}
	}
	if s.hasDefault {
		return s.defaultPolicy, true
	}
	return TrafficPolicySelection{}, false
}

func (s *TrafficPolicySelector) resolveRoute(msgID uint32) (string, bool) {
	for _, route := range s.routes {
		maxMsgID := route.MsgIDMax
		if maxMsgID == 0 {
			maxMsgID = route.MsgIDMin
		}
		if route.MsgIDMin <= msgID && msgID <= maxMsgID {
			return route.Name, true
		}
	}
	return "", false
}

func (p compiledTrafficPolicy) matches(clientID string, msgID uint32, routeName string, routeFound bool) bool {
	if len(p.clientIDs) > 0 {
		if _, exists := p.clientIDs[clientID]; !exists {
			return false
		}
	}
	if p.match.MsgIDMin != 0 {
		maxMsgID := p.match.MsgIDMax
		if maxMsgID == 0 {
			maxMsgID = p.match.MsgIDMin
		}
		if msgID < p.match.MsgIDMin || msgID > maxMsgID {
			return false
		}
	}
	if len(p.routes) > 0 {
		if !routeFound {
			return false
		}
		if _, exists := p.routes[routeName]; !exists {
			return false
		}
	}
	return true
}

func cloneTrafficPolicyMatch(match TrafficPolicyMatch) TrafficPolicyMatch {
	match.ClientIDs = append([]string(nil), match.ClientIDs...)
	match.Routes = append([]string(nil), match.Routes...)
	return match
}

func makeStringSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}
