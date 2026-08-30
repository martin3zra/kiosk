package kiosk

// This file defines kiosk's domain events. They're dispatched through the
// forge/events.Dispatcher a host passes via Options.Events, which is
// parameterised on *playsql.Tx — so a listener runs inside the transaction
// that raised the event and can roll it back by returning an error.
//
// Subscribe from the host:
//
//	evts := events.NewDispatcher[*playsql.Tx]()
//	events.On(evts, func(ctx context.Context, tx *playsql.Tx, e kiosk.CompanyCreated) error {
//		// … provision the company's default rows through tx …
//		return nil
//	})
//	kiosk.Register(router, kiosk.Options{ /* … */ Events: evts })

// CompanyCreated is dispatched inside companiesCreate's transaction, right
// after the companies row is inserted and before the owner is attached or
// invited. A listener that returns an error rolls the whole company
// creation back.
type CompanyCreated struct {
	CompanyID int
	Name      string
}
