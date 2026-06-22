package contracts

import (
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/model"
)

// DriftSeverity classifies a drift as breaking or non-breaking.
type DriftSeverity string

const (
	// Breaking indicates a backwards-incompatible change: removed field,
	// changed type, removed required field.
	Breaking DriftSeverity = "breaking"
	// NonBreaking indicates a backwards-compatible change: added optional
	// field, relaxed required-ness.
	NonBreaking DriftSeverity = "non-breaking"
)

// DriftKind identifies the specific type of structural drift.
type DriftKind string

const (
	// DriftFieldRemoved — a field present in the producer is missing from the
	// consumer's expectation, or vice versa. Breaking.
	DriftFieldRemoved DriftKind = "field_removed"
	// DriftFieldAdded — a field is present in one side but not the other, and
	// it is optional. Non-breaking.
	DriftFieldAdded DriftKind = "field_added"
	// DriftTypeChanged — a field exists on both sides but the types differ.
	// Breaking.
	DriftTypeChanged DriftKind = "type_changed"
	// DriftRequiredChanged — a field's required-ness changed. Going from
	// optional to required is breaking; required to optional is non-breaking.
	DriftRequiredChanged DriftKind = "required_changed"
)

// Drift is a single detected structural mismatch between a producer and
// consumer contract surface.
type Drift struct {
	// ProducerNodeID is the producer side of the link.
	ProducerNodeID model.NodeId `json:"producer_node_id"`
	// ConsumerNodeID is the consumer side of the link.
	ConsumerNodeID model.NodeId `json:"consumer_node_id"`
	// ServiceKey is the logical service both sides reference.
	ServiceKey string `json:"service_key"`
	// Kind is the specific drift type.
	Kind DriftKind `json:"kind"`
	// Severity classifies the drift as breaking or non-breaking.
	Severity DriftSeverity `json:"severity"`
	// FieldName is the name of the field where drift was detected.
	FieldName string `json:"field_name"`
	// Detail is a human-readable description of the drift.
	Detail string `json:"detail"`
}

// detectDrifts compares the producer and consumer field shapes of each link
// and returns any structural drifts found.
func detectDrifts(links []ContractLink) []Drift {
	var drifts []Drift

	for _, link := range links {
		ld := compareSurfaces(link)
		drifts = append(drifts, ld...)
	}

	// Sort drifts for determinism: by ServiceKey, then Severity (breaking
	// first), then Kind, then FieldName.
	sort.Slice(drifts, func(i, j int) bool {
		a, b := drifts[i], drifts[j]
		if a.ServiceKey != b.ServiceKey {
			return a.ServiceKey < b.ServiceKey
		}
		if a.Severity != b.Severity {
			return a.Severity < b.Severity // "breaking" < "non-breaking"
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.FieldName < b.FieldName
	})

	return drifts
}

// compareSurfaces performs a structural diff between a link's producer and
// consumer fields, emitting one Drift per mismatch.
func compareSurfaces(link ContractLink) []Drift {
	var drifts []Drift

	producerMap := indexFields(link.ProducerFields)
	consumerMap := indexFields(link.ConsumerFields)

	// Check for fields in producer but missing from consumer.
	for name, pf := range producerMap {
		cf, inConsumer := consumerMap[name]
		if !inConsumer {
			if pf.Required {
				drifts = append(drifts, Drift{
					ProducerNodeID: link.ProducerNodeID,
					ConsumerNodeID: link.ConsumerNodeID,
					ServiceKey:     link.ServiceKey,
					Kind:           DriftFieldRemoved,
					Severity:       Breaking,
					FieldName:      name,
					Detail:         fmt.Sprintf("required field %q present in producer but missing from consumer", name),
				})
			} else {
				drifts = append(drifts, Drift{
					ProducerNodeID: link.ProducerNodeID,
					ConsumerNodeID: link.ConsumerNodeID,
					ServiceKey:     link.ServiceKey,
					Kind:           DriftFieldAdded,
					Severity:       NonBreaking,
					FieldName:      name,
					Detail:         fmt.Sprintf("optional field %q present in producer but missing from consumer", name),
				})
			}
			continue
		}

		// Both sides have the field — check type.
		if pf.Type != cf.Type {
			drifts = append(drifts, Drift{
				ProducerNodeID: link.ProducerNodeID,
				ConsumerNodeID: link.ConsumerNodeID,
				ServiceKey:     link.ServiceKey,
				Kind:           DriftTypeChanged,
				Severity:       Breaking,
				FieldName:      name,
				Detail:         fmt.Sprintf("field %q type changed: producer=%q, consumer=%q", name, pf.Type, cf.Type),
			})
		}

		// Check required-ness change.
		if pf.Required != cf.Required {
			sev := NonBreaking
			detail := fmt.Sprintf("field %q required-ness changed: producer=%v, consumer=%v", name, pf.Required, cf.Required)
			if !pf.Required && cf.Required {
				// Consumer expects required but producer says optional → breaking
				// for consumers that depend on it being always present.
				sev = Breaking
			}
			drifts = append(drifts, Drift{
				ProducerNodeID: link.ProducerNodeID,
				ConsumerNodeID: link.ConsumerNodeID,
				ServiceKey:     link.ServiceKey,
				Kind:           DriftRequiredChanged,
				Severity:       sev,
				FieldName:      name,
				Detail:         detail,
			})
		}
	}

	// Check for fields in consumer but missing from producer (consumer
	// expects something the producer doesn't provide → breaking).
	for name := range consumerMap {
		if _, inProducer := producerMap[name]; !inProducer {
			drifts = append(drifts, Drift{
				ProducerNodeID: link.ProducerNodeID,
				ConsumerNodeID: link.ConsumerNodeID,
				ServiceKey:     link.ServiceKey,
				Kind:           DriftFieldRemoved,
				Severity:       Breaking,
				FieldName:      name,
				Detail:         fmt.Sprintf("field %q expected by consumer but missing from producer", name),
			})
		}
	}

	return drifts
}

// indexFields builds a name→Field lookup map.
func indexFields(fields []Field) map[string]Field {
	m := make(map[string]Field, len(fields))
	for _, f := range fields {
		m[f.Name] = f
	}
	return m
}
