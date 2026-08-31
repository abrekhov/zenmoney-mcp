package zenmoney

import (
	"context"
	"testing"
)

type fakeAPI struct {
	requests  []DiffRequest
	responses []DiffResponse
}

func (f *fakeAPI) Diff(_ context.Context, request DiffRequest) (DiffResponse, error) {
	f.requests = append(f.requests, request)
	response := f.responses[0]
	f.responses = f.responses[1:]
	return response, nil
}

func (*fakeAPI) Suggest(context.Context, []SuggestRequest) ([]SuggestResponse, error) {
	return nil, nil
}

func TestStateSyncAndAddTransaction(t *testing.T) {
	instrumentID := 1
	api := &fakeAPI{responses: []DiffResponse{
		{
			ServerTimestamp: 100,
			Account:         []Account{{ID: "account-1", Title: "Card", Instrument: &instrumentID}},
			Tag:             []Tag{{ID: "tag-1", Title: "Food"}},
			Instrument:      []Instrument{{ID: 1, ShortTitle: "RUB"}},
			User:            []User{{ID: 7, Currency: 1}},
		},
		{ServerTimestamp: 101},
	}}
	state := NewState(api)
	snapshot, err := state.Sync(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Synced || len(snapshot.Accounts) != 1 || snapshot.ServerTimestamp != 100 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if len(api.requests[0].ForceFetch) == 0 || api.requests[0].ServerTimestamp != 0 {
		t.Fatalf("first sync was not full: %+v", api.requests[0])
	}

	transaction := Transaction{ID: "transaction-1", User: 7, Date: "2026-08-30", IncomeAccount: "account-1", OutcomeAccount: "account-1", Outcome: 42}
	snapshot, err = state.AddTransaction(context.Background(), transaction)
	if err != nil {
		t.Fatal(err)
	}
	if len(api.requests[1].Transaction) != 1 || api.requests[1].Transaction[0].Deleted {
		t.Fatalf("unexpected mutation request: %+v", api.requests[1])
	}
	if len(snapshot.Transactions) != 1 || snapshot.Transactions[0].ID != transaction.ID || snapshot.ServerTimestamp != 101 {
		t.Fatalf("transaction not merged: %+v", snapshot)
	}
}

func TestApplyServerDeletionOnlyChangesLocalCache(t *testing.T) {
	api := &fakeAPI{responses: []DiffResponse{
		{ServerTimestamp: 1, Transaction: []Transaction{{ID: "old", Date: "2026-01-01"}}},
		{ServerTimestamp: 2, Deletion: []Deletion{{Object: "transaction", ID: "old"}}},
	}}
	state := NewState(api)
	if _, err := state.Sync(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	snapshot, err := state.Sync(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Transactions) != 0 {
		t.Fatalf("server deletion was not reflected locally: %+v", snapshot.Transactions)
	}
	if len(api.requests[1].Deletion) != 0 {
		t.Fatal("client sent a deletion to ZenMoney")
	}
}
