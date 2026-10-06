package diff

// Severity ranks how concerning a change is, used to sort/filter reports.
type Severity int

const (
	SeverityLow Severity = iota
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

func (s Severity) String() string {
	switch s {
	case SeverityCritical:
		return "critical"
	case SeverityHigh:
		return "high"
	case SeverityMedium:
		return "medium"
	default:
		return "low"
	}
}

// Score assigns a severity to a single Change based on structural
// features, per the rubric in the product spec section 6.5(5). This is a
// deliberately explicit, testable function rather than a learned model.
func Score(c Change) Severity {
	switch c.Type {
	case ErrorAppeared:
		return SeverityCritical
	case StatusChanged:
		if before, ok := c.Before.(int); ok {
			if after, ok := c.After.(int); ok {
				if before < 400 && after >= 500 {
					return SeverityCritical
				}
				if before < 400 && after >= 400 {
					return SeverityHigh
				}
			}
		}
		return SeverityMedium
	case FieldRemoved:
		return SeverityHigh
	case TypeChanged:
		return SeverityHigh
	case FieldAdded:
		return SeverityLow
	case ArrayLengthChanged:
		return SeverityMedium
	case ArrayOrderChanged:
		return SeverityLow
	case ValueChanged:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// MaxSeverity returns the highest severity among changes, or SeverityLow
// if changes is empty.
func MaxSeverity(changes []Change) Severity {
	max := SeverityLow
	for _, c := range changes {
		if s := Score(c); s > max {
			max = s
		}
	}
	return max
}
