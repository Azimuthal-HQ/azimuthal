package workflow

import "errors"

// ErrNotFound is returned when a workflow object cannot be located.
var ErrNotFound = errors.New("not found")

// ErrInvalidTransition is returned when the requested state change is not
// permitted by the workflow definition.
var ErrInvalidTransition = errors.New("invalid workflow transition")

// ErrNoWorkflow is returned when an entity has no workflow assigned.
var ErrNoWorkflow = errors.New("no workflow assigned")
