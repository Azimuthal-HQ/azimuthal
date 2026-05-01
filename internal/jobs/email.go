package jobs

import (
	"context"
	"fmt"

	"github.com/riverqueue/river"

	"github.com/Azimuthal-HQ/azimuthal/internal/core/email"
)

// EmailArgs holds the arguments for an email delivery job.
// It implements river.JobArgs.
type EmailArgs struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
}

// Kind returns the unique job kind identifier used by River.
func (EmailArgs) Kind() string { return "email" }

// EmailWorker processes EmailArgs jobs by delivering the email via the
// configured Sender.
type EmailWorker struct {
	river.WorkerDefaults[EmailArgs]
	sender email.Sender
}

// NewEmailWorker creates an EmailWorker backed by the given Sender.
func NewEmailWorker(sender email.Sender) *EmailWorker {
	return &EmailWorker{sender: sender}
}

// Work delivers the email described in the job arguments.
func (w *EmailWorker) Work(ctx context.Context, job *river.Job[EmailArgs]) error {
	args := job.Args
	msg := email.Message{
		From:    args.From,
		To:      args.To,
		Subject: args.Subject,
		Body:    args.Body,
	}
	if err := w.sender.Send(ctx, msg); err != nil {
		return fmt.Errorf("delivering email to %v: %w", args.To, err)
	}
	return nil
}
