package email

import "context"

type Sender interface {
	SendVerification(ctx context.Context, to, verifyURL string) error
	SendConnectionRequest(ctx context.Context, to, company, summary, dashboardURL string) error
	SendConnectionApproved(ctx context.Context, to, candidateNote string) error
	SendConnectionDeclined(ctx context.Context, to string) error
}

type LogSender struct{}

func (LogSender) SendVerification(_ context.Context, to, verifyURL string) error {
	println("[agentlist] verification email")
	println("  to:", to)
	println("  link:", verifyURL)
	return nil
}

func (LogSender) SendConnectionRequest(_ context.Context, to, company, summary, dashboardURL string) error {
	println("[agentlist] connection request email")
	println("  to:", to)
	println("  company:", company)
	println("  summary:", summary)
	println("  dashboard:", dashboardURL)
	return nil
}

func (LogSender) SendConnectionApproved(_ context.Context, to, candidateNote string) error {
	println("[agentlist] connection approved email")
	println("  to:", to)
	println("  note:", candidateNote)
	return nil
}

func (LogSender) SendConnectionDeclined(_ context.Context, to string) error {
	println("[agentlist] connection declined email")
	println("  to:", to)
	return nil
}
