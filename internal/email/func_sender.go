package email

import "context"

type FuncSender func(ctx context.Context, to, verifyURL string) error

func (f FuncSender) SendVerification(ctx context.Context, to, verifyURL string) error {
	return f(ctx, to, verifyURL)
}

func (f FuncSender) SendConnectionRequest(ctx context.Context, to, company, summary, dashboardURL string) error {
	return nil
}

func (f FuncSender) SendConnectionApproved(ctx context.Context, to, candidateNote string) error {
	return nil
}

func (f FuncSender) SendConnectionDeclined(ctx context.Context, to string) error {
	return nil
}
