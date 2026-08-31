package mcpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/abrekhov/zenmoney-mcp/internal/zenmoney"
)

const Version = "1.0.0"

type Service struct {
	api   zenmoney.API
	state *zenmoney.State
}

func New(api zenmoney.API, state *zenmoney.State) *mcp.Server {
	svc := &Service{api: api, state: state}
	server := mcp.NewServer(&mcp.Implementation{Name: "zenmoney-mcp", Version: Version}, nil)
	svc.register(server)
	return server
}

func (s *Service) register(server *mcp.Server) {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(false)}
	additive := &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)}

	mcp.AddTool(server, &mcp.Tool{
		Name: "sync_data", Description: "Synchronize cached data with ZenMoney. Other read tools sync automatically; use force_full only to re-download all data.", Annotations: readOnly,
	}, s.syncData)
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_accounts", Description: "List ZenMoney wallets, cards, cash, and other accounts with balances and currencies.", Annotations: readOnly,
	}, s.listAccounts)
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_categories", Description: "List expense and income categories (tags), including parent-child hierarchy.", Annotations: readOnly,
	}, s.listCategories)
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_merchants", Description: "List known ZenMoney merchants/payees, optionally filtered by title.", Annotations: readOnly,
	}, s.listMerchants)
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_transactions", Description: "List and filter ZenMoney transactions. Defaults to the last 30 days.", Annotations: readOnly,
	}, s.listTransactions)
	mcp.AddTool(server, &mcp.Tool{
		Name: "suggest_category", Description: "Ask ZenMoney to suggest a category and merchant for a payee name. This does not modify data.", Annotations: readOnly,
	}, s.suggestCategory)
	mcp.AddTool(server, &mcp.Tool{
		Name: "add_expense", Description: "Create a new expense in ZenMoney. This mutates the account by adding one transaction; it never deletes data.", Annotations: additive,
	}, s.addExpense)
	mcp.AddTool(server, &mcp.Tool{
		Name: "add_income", Description: "Create a new income transaction in ZenMoney. This mutates the account by adding one transaction; it never deletes data.", Annotations: additive,
	}, s.addIncome)
	mcp.AddTool(server, &mcp.Tool{
		Name: "add_transfer", Description: "Create a new transfer between two ZenMoney accounts. This mutates the account by adding one transaction; it never deletes data.", Annotations: additive,
	}, s.addTransfer)
}

type syncInput struct {
	ForceFull bool `json:"force_full,omitempty" jsonschema:"force a complete re-download instead of an incremental sync"`
}

func (s *Service) syncData(ctx context.Context, _ *mcp.CallToolRequest, in syncInput) (*mcp.CallToolResult, any, error) {
	snapshot, err := s.state.Sync(ctx, in.ForceFull)
	if err != nil {
		return nil, nil, fmt.Errorf("sync ZenMoney data: %w", err)
	}
	return nil, summary(snapshot), nil
}

type listAccountsInput struct {
	IncludeArchived bool `json:"include_archived,omitempty" jsonschema:"include archived accounts"`
}

func (s *Service) listAccounts(ctx context.Context, _ *mcp.CallToolRequest, in listAccountsInput) (*mcp.CallToolResult, any, error) {
	if err := s.state.EnsureSynced(ctx); err != nil {
		return nil, nil, fmt.Errorf("sync ZenMoney data: %w", err)
	}
	snapshot := s.state.Snapshot()
	instruments := instrumentsByID(snapshot)
	companies := companiesByID(snapshot)
	accounts := make([]map[string]any, 0, len(snapshot.Accounts))
	for _, account := range snapshot.Accounts {
		if account.Archive && !in.IncludeArchived {
			continue
		}
		item := map[string]any{
			"id": account.ID, "title": account.Title, "type": account.Type,
			"balance": account.Balance, "archived": account.Archive, "in_balance": account.InBalance,
		}
		if account.Instrument != nil {
			if instrument, ok := instruments[*account.Instrument]; ok {
				item["currency"] = map[string]any{"id": instrument.ID, "title": instrument.Title, "short_title": instrument.ShortTitle, "symbol": instrument.Symbol}
			}
		}
		if account.Company != nil {
			if company, ok := companies[*account.Company]; ok {
				item["company"] = company.Title
			}
		}
		accounts = append(accounts, item)
	}
	return nil, map[string]any{"count": len(accounts), "accounts": accounts}, nil
}

type emptyInput struct{}

func (s *Service) listCategories(ctx context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, any, error) {
	if err := s.state.EnsureSynced(ctx); err != nil {
		return nil, nil, fmt.Errorf("sync ZenMoney data: %w", err)
	}
	snapshot := s.state.Snapshot()
	children := make(map[string][]zenmoney.Tag)
	var roots []zenmoney.Tag
	for _, tag := range snapshot.Tags {
		if tag.Parent == nil || *tag.Parent == "" {
			roots = append(roots, tag)
		} else {
			children[*tag.Parent] = append(children[*tag.Parent], tag)
		}
	}
	result := make([]map[string]any, 0, len(roots))
	for _, root := range roots {
		entry := categoryMap(root)
		items := make([]map[string]any, 0, len(children[root.ID]))
		for _, child := range children[root.ID] {
			items = append(items, categoryMap(child))
		}
		entry["children"] = items
		result = append(result, entry)
	}
	return nil, map[string]any{"count": len(snapshot.Tags), "categories": result}, nil
}

type listMerchantsInput struct {
	Query string `json:"query,omitempty" jsonschema:"case-insensitive substring to match in merchant titles"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum merchants to return; default 100, maximum 500"`
}

func (s *Service) listMerchants(ctx context.Context, _ *mcp.CallToolRequest, in listMerchantsInput) (*mcp.CallToolResult, any, error) {
	if err := s.state.EnsureSynced(ctx); err != nil {
		return nil, nil, fmt.Errorf("sync ZenMoney data: %w", err)
	}
	limit, err := boundedLimit(in.Limit, 100, 500)
	if err != nil {
		return nil, nil, err
	}
	query := strings.ToLower(strings.TrimSpace(in.Query))
	var result []map[string]any
	for _, merchant := range s.state.Snapshot().Merchants {
		if query != "" && !strings.Contains(strings.ToLower(merchant.Title), query) {
			continue
		}
		result = append(result, map[string]any{"id": merchant.ID, "title": merchant.Title})
		if len(result) == limit {
			break
		}
	}
	return nil, map[string]any{"count": len(result), "merchants": result}, nil
}

type listTransactionsInput struct {
	Days      int    `json:"days,omitempty" jsonschema:"days to look back when no explicit range is given; default 30"`
	StartDate string `json:"start_date,omitempty" jsonschema:"inclusive range start in YYYY-MM-DD"`
	EndDate   string `json:"end_date,omitempty" jsonschema:"inclusive range end in YYYY-MM-DD"`
	Account   string `json:"account,omitempty" jsonschema:"account title or ID"`
	Category  string `json:"category,omitempty" jsonschema:"category title or ID"`
	Limit     int    `json:"limit,omitempty" jsonschema:"maximum transactions to return; default 50, maximum 500"`
}

func (s *Service) listTransactions(ctx context.Context, _ *mcp.CallToolRequest, in listTransactionsInput) (*mcp.CallToolResult, any, error) {
	if err := s.state.EnsureSynced(ctx); err != nil {
		return nil, nil, fmt.Errorf("sync ZenMoney data: %w", err)
	}
	limit, err := boundedLimit(in.Limit, 50, 500)
	if err != nil {
		return nil, nil, err
	}
	snapshot := s.state.Snapshot()
	start, end, err := transactionRange(in)
	if err != nil {
		return nil, nil, err
	}
	var accountID string
	if strings.TrimSpace(in.Account) != "" {
		account, err := resolveAccount(snapshot, in.Account)
		if err != nil {
			return nil, nil, err
		}
		accountID = account.ID
	}
	var categoryID string
	if strings.TrimSpace(in.Category) != "" {
		tag, err := resolveTag(snapshot, in.Category)
		if err != nil {
			return nil, nil, err
		}
		categoryID = tag.ID
	}
	accounts := accountsByID(snapshot)
	instruments := instrumentsByID(snapshot)
	tags := tagsByID(snapshot)
	var result []map[string]any
	for _, transaction := range snapshot.Transactions {
		if transaction.Date < start || transaction.Date > end {
			continue
		}
		if accountID != "" && transaction.IncomeAccount != accountID && transaction.OutcomeAccount != accountID {
			continue
		}
		if categoryID != "" && !contains(transaction.Tag, categoryID) {
			continue
		}
		item := transactionMap(transaction, accounts, instruments, tags)
		result = append(result, item)
		if len(result) == limit {
			break
		}
	}
	return nil, map[string]any{"count": len(result), "start_date": start, "end_date": end, "transactions": result}, nil
}

type suggestInput struct {
	Payee string `json:"payee" jsonschema:"payee or merchant name to categorize"`
}

func (s *Service) suggestCategory(ctx context.Context, _ *mcp.CallToolRequest, in suggestInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.Payee) == "" {
		return nil, nil, fmt.Errorf("payee is required")
	}
	if err := s.state.EnsureSynced(ctx); err != nil {
		return nil, nil, fmt.Errorf("sync ZenMoney data: %w", err)
	}
	response, err := s.api.Suggest(ctx, []zenmoney.SuggestRequest{{Payee: in.Payee}})
	if err != nil {
		return nil, nil, fmt.Errorf("get ZenMoney suggestion: %w", err)
	}
	if len(response) == 0 {
		return nil, map[string]any{"payee": in.Payee, "suggestion": nil}, nil
	}
	snapshot := s.state.Snapshot()
	tags := tagsByID(snapshot)
	merchants := merchantsByID(snapshot)
	item := map[string]any{"tag_ids": response[0].Tag}
	var categoryNames []string
	for _, id := range response[0].Tag {
		if tag, ok := tags[id]; ok {
			categoryNames = append(categoryNames, tag.Title)
		}
	}
	item["categories"] = categoryNames
	if response[0].Merchant != nil {
		item["merchant_id"] = *response[0].Merchant
		if merchant, ok := merchants[*response[0].Merchant]; ok {
			item["merchant"] = merchant.Title
		}
	}
	if response[0].Payee != nil {
		item["normalized_payee"] = *response[0].Payee
	}
	return nil, map[string]any{"payee": in.Payee, "suggestion": item}, nil
}

type addCommonInput struct {
	Account  string  `json:"account" jsonschema:"account title or ID"`
	Amount   float64 `json:"amount" jsonschema:"positive transaction amount"`
	Date     string  `json:"date" jsonschema:"transaction date in YYYY-MM-DD"`
	Category string  `json:"category,omitempty" jsonschema:"category title or ID"`
	Payee    string  `json:"payee,omitempty" jsonschema:"payee or payer name"`
	Comment  string  `json:"comment,omitempty" jsonschema:"transaction comment"`
}

func (s *Service) addExpense(ctx context.Context, req *mcp.CallToolRequest, in addCommonInput) (*mcp.CallToolResult, any, error) {
	if err := requireWriteScope(req); err != nil {
		return nil, nil, err
	}
	return s.addIncomeOrExpense(ctx, in, false)
}

func (s *Service) addIncome(ctx context.Context, req *mcp.CallToolRequest, in addCommonInput) (*mcp.CallToolResult, any, error) {
	if err := requireWriteScope(req); err != nil {
		return nil, nil, err
	}
	return s.addIncomeOrExpense(ctx, in, true)
}

func (s *Service) addIncomeOrExpense(ctx context.Context, in addCommonInput, income bool) (*mcp.CallToolResult, any, error) {
	if err := validateAmountAndDate(in.Amount, in.Date); err != nil {
		return nil, nil, err
	}
	if err := s.state.EnsureSynced(ctx); err != nil {
		return nil, nil, fmt.Errorf("sync ZenMoney data: %w", err)
	}
	snapshot := s.state.Snapshot()
	account, err := resolveAccount(snapshot, in.Account)
	if err != nil {
		return nil, nil, err
	}
	user, err := primaryUser(snapshot)
	if err != nil {
		return nil, nil, err
	}
	instrument := user.Currency
	if account.Instrument != nil {
		instrument = *account.Instrument
	}
	var tagIDs []string
	if strings.TrimSpace(in.Category) != "" {
		tag, err := resolveTag(snapshot, in.Category)
		if err != nil {
			return nil, nil, err
		}
		tagIDs = []string{tag.ID}
	}
	now := time.Now().Unix()
	transaction := baseTransaction(user.ID, now, in.Date)
	transaction.IncomeInstrument = instrument
	transaction.OutcomeInstrument = instrument
	transaction.IncomeAccount = account.ID
	transaction.OutcomeAccount = account.ID
	transaction.Tag = tagIDs
	transaction.Payee = optionalString(in.Payee)
	transaction.Comment = optionalString(in.Comment)
	if income {
		transaction.Income = in.Amount
	} else {
		transaction.Outcome = in.Amount
	}
	if _, err := s.state.AddTransaction(ctx, transaction); err != nil {
		return nil, nil, fmt.Errorf("add ZenMoney transaction: %w", err)
	}
	kind := "expense"
	if income {
		kind = "income"
	}
	return nil, map[string]any{"created": true, "type": kind, "transaction_id": transaction.ID, "account": account.Title, "amount": in.Amount, "date": in.Date}, nil
}

type addTransferInput struct {
	FromAccount   string  `json:"from_account" jsonschema:"source account title or ID"`
	ToAccount     string  `json:"to_account" jsonschema:"destination account title or ID"`
	Amount        float64 `json:"amount,omitempty" jsonschema:"same-currency amount, or alias for outcome_amount"`
	OutcomeAmount float64 `json:"outcome_amount,omitempty" jsonschema:"amount debited in source currency"`
	IncomeAmount  float64 `json:"income_amount,omitempty" jsonschema:"amount credited in destination currency; required for cross-currency transfers"`
	Date          string  `json:"date" jsonschema:"transaction date in YYYY-MM-DD"`
	Comment       string  `json:"comment,omitempty" jsonschema:"transfer comment"`
}

func (s *Service) addTransfer(ctx context.Context, req *mcp.CallToolRequest, in addTransferInput) (*mcp.CallToolResult, any, error) {
	if err := requireWriteScope(req); err != nil {
		return nil, nil, err
	}
	outcome := in.OutcomeAmount
	if outcome == 0 {
		outcome = in.Amount
	}
	if err := validateAmountAndDate(outcome, in.Date); err != nil {
		return nil, nil, err
	}
	if err := s.state.EnsureSynced(ctx); err != nil {
		return nil, nil, fmt.Errorf("sync ZenMoney data: %w", err)
	}
	snapshot := s.state.Snapshot()
	from, err := resolveAccount(snapshot, in.FromAccount)
	if err != nil {
		return nil, nil, fmt.Errorf("source %w", err)
	}
	to, err := resolveAccount(snapshot, in.ToAccount)
	if err != nil {
		return nil, nil, fmt.Errorf("destination %w", err)
	}
	if from.ID == to.ID {
		return nil, nil, fmt.Errorf("source and destination accounts must differ")
	}
	user, err := primaryUser(snapshot)
	if err != nil {
		return nil, nil, err
	}
	outcomeInstrument, incomeInstrument := user.Currency, user.Currency
	if from.Instrument != nil {
		outcomeInstrument = *from.Instrument
	}
	if to.Instrument != nil {
		incomeInstrument = *to.Instrument
	}
	income := in.IncomeAmount
	if outcomeInstrument == incomeInstrument && income == 0 {
		income = outcome
	}
	if income <= 0 || math.IsNaN(income) || math.IsInf(income, 0) {
		return nil, nil, fmt.Errorf("income_amount must be positive for a cross-currency transfer")
	}
	now := time.Now().Unix()
	transaction := baseTransaction(user.ID, now, in.Date)
	transaction.OutcomeAccount, transaction.OutcomeInstrument, transaction.Outcome = from.ID, outcomeInstrument, outcome
	transaction.IncomeAccount, transaction.IncomeInstrument, transaction.Income = to.ID, incomeInstrument, income
	transaction.Comment = optionalString(in.Comment)
	if _, err := s.state.AddTransaction(ctx, transaction); err != nil {
		return nil, nil, fmt.Errorf("add ZenMoney transfer: %w", err)
	}
	return nil, map[string]any{"created": true, "type": "transfer", "transaction_id": transaction.ID, "from_account": from.Title, "to_account": to.Title, "outcome_amount": outcome, "income_amount": income, "date": in.Date}, nil
}

func summary(snapshot zenmoney.Snapshot) map[string]any {
	active := 0
	for _, account := range snapshot.Accounts {
		if !account.Archive {
			active++
		}
	}
	return map[string]any{
		"synced": true, "server_timestamp": snapshot.ServerTimestamp,
		"accounts": len(snapshot.Accounts), "active_accounts": active,
		"categories": len(snapshot.Tags), "merchants": len(snapshot.Merchants),
		"transactions": len(snapshot.Transactions), "currencies": len(snapshot.Instruments),
	}
}

func categoryMap(tag zenmoney.Tag) map[string]any {
	return map[string]any{"id": tag.ID, "title": tag.Title, "expense": tag.ShowOutcome, "income": tag.ShowIncome}
}

func transactionRange(in listTransactionsInput) (string, string, error) {
	today := time.Now().UTC()
	if in.StartDate != "" || in.EndDate != "" {
		start := "0001-01-01"
		end := today.Format(time.DateOnly)
		var err error
		if in.StartDate != "" {
			if _, err = time.Parse(time.DateOnly, in.StartDate); err != nil {
				return "", "", fmt.Errorf("invalid start_date: use YYYY-MM-DD")
			}
			start = in.StartDate
		}
		if in.EndDate != "" {
			if _, err = time.Parse(time.DateOnly, in.EndDate); err != nil {
				return "", "", fmt.Errorf("invalid end_date: use YYYY-MM-DD")
			}
			end = in.EndDate
		}
		if start > end {
			return "", "", fmt.Errorf("start_date must be on or before end_date")
		}
		return start, end, nil
	}
	days := in.Days
	if days == 0 {
		days = 30
	}
	if days < 1 || days > 36500 {
		return "", "", fmt.Errorf("days must be between 1 and 36500")
	}
	return today.AddDate(0, 0, -days).Format(time.DateOnly), today.Format(time.DateOnly), nil
}

func transactionMap(transaction zenmoney.Transaction, accounts map[string]zenmoney.Account, instruments map[int]zenmoney.Instrument, tags map[string]zenmoney.Tag) map[string]any {
	kind := "other"
	if transaction.IncomeAccount != transaction.OutcomeAccount {
		kind = "transfer"
	} else if transaction.Outcome > 0 && transaction.Income == 0 {
		kind = "expense"
	} else if transaction.Income > 0 && transaction.Outcome == 0 {
		kind = "income"
	}
	item := map[string]any{
		"id": transaction.ID, "date": transaction.Date, "type": kind,
		"income": transaction.Income, "outcome": transaction.Outcome,
		"income_account_id": transaction.IncomeAccount, "outcome_account_id": transaction.OutcomeAccount,
		"payee": transaction.Payee, "comment": transaction.Comment,
	}
	if account, ok := accounts[transaction.IncomeAccount]; ok {
		item["income_account"] = account.Title
	}
	if account, ok := accounts[transaction.OutcomeAccount]; ok {
		item["outcome_account"] = account.Title
	}
	if instrument, ok := instruments[transaction.IncomeInstrument]; ok {
		item["income_currency"] = instrument.ShortTitle
	}
	if instrument, ok := instruments[transaction.OutcomeInstrument]; ok {
		item["outcome_currency"] = instrument.ShortTitle
	}
	var categories []string
	for _, id := range transaction.Tag {
		if tag, ok := tags[id]; ok {
			categories = append(categories, tag.Title)
		} else {
			categories = append(categories, id)
		}
	}
	item["categories"] = categories
	return item
}

func resolveAccount(snapshot zenmoney.Snapshot, value string) (zenmoney.Account, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return zenmoney.Account{}, fmt.Errorf("account is required")
	}
	return resolveNamed(value, snapshot.Accounts, func(item zenmoney.Account) string { return item.ID }, func(item zenmoney.Account) string { return item.Title }, "account")
}

func resolveTag(snapshot zenmoney.Snapshot, value string) (zenmoney.Tag, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return zenmoney.Tag{}, fmt.Errorf("category is required")
	}
	return resolveNamed(value, snapshot.Tags, func(item zenmoney.Tag) string { return item.ID }, func(item zenmoney.Tag) string { return item.Title }, "category")
}

func resolveNamed[T any](value string, items []T, id func(T) string, title func(T) string, kind string) (T, error) {
	var zero T
	for _, item := range items {
		if id(item) == value {
			return item, nil
		}
	}
	lower := strings.ToLower(value)
	var exact []T
	for _, item := range items {
		if strings.ToLower(title(item)) == lower {
			exact = append(exact, item)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	var partial []T
	for _, item := range items {
		if strings.Contains(strings.ToLower(title(item)), lower) {
			partial = append(partial, item)
		}
	}
	if len(partial) == 1 {
		return partial[0], nil
	}
	if len(exact) > 1 || len(partial) > 1 {
		matches := partial
		if len(exact) > 1 {
			matches = exact
		}
		var names []string
		for _, item := range matches {
			names = append(names, fmt.Sprintf("%s (%s)", title(item), id(item)))
		}
		sort.Strings(names)
		return zero, fmt.Errorf("ambiguous %s %q; matches: %s", kind, value, strings.Join(names, ", "))
	}
	return zero, fmt.Errorf("%s %q not found", kind, value)
}

func primaryUser(snapshot zenmoney.Snapshot) (zenmoney.User, error) {
	for _, user := range snapshot.Users {
		if user.Parent == nil {
			return user, nil
		}
	}
	if len(snapshot.Users) > 0 {
		return snapshot.Users[0], nil
	}
	return zenmoney.User{}, fmt.Errorf("ZenMoney user was not returned by sync")
}

func baseTransaction(user int, now int64, date string) zenmoney.Transaction {
	return zenmoney.Transaction{ID: newUUID(), Changed: now, Created: now, User: user, Viewed: false, Date: date}
}

func newUUID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(data[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func validateAmountAndDate(amount float64, date string) error {
	if amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return fmt.Errorf("amount must be a finite positive number")
	}
	if _, err := time.Parse(time.DateOnly, date); err != nil {
		return fmt.Errorf("invalid date: use YYYY-MM-DD")
	}
	return nil
}

func boundedLimit(value, defaultValue, maximum int) (int, error) {
	if value == 0 {
		return defaultValue, nil
	}
	if value < 1 || value > maximum {
		return 0, fmt.Errorf("limit must be between 1 and %d", maximum)
	}
	return value, nil
}

func boolPtr(value bool) *bool { return &value }

func requireWriteScope(req *mcp.CallToolRequest) error {
	// Stdio sessions have no bearer token and are trusted local processes.
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return nil
	}
	for _, scope := range req.Extra.TokenInfo.Scopes {
		if scope == "zenmoney:write" {
			return nil
		}
	}
	return fmt.Errorf("OAuth scope zenmoney:write is required")
}

func optionalString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func contains(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}

func accountsByID(snapshot zenmoney.Snapshot) map[string]zenmoney.Account {
	result := make(map[string]zenmoney.Account, len(snapshot.Accounts))
	for _, item := range snapshot.Accounts {
		result[item.ID] = item
	}
	return result
}
func instrumentsByID(snapshot zenmoney.Snapshot) map[int]zenmoney.Instrument {
	result := make(map[int]zenmoney.Instrument, len(snapshot.Instruments))
	for _, item := range snapshot.Instruments {
		result[item.ID] = item
	}
	return result
}
func companiesByID(snapshot zenmoney.Snapshot) map[int]zenmoney.Company {
	result := make(map[int]zenmoney.Company, len(snapshot.Companies))
	for _, item := range snapshot.Companies {
		result[item.ID] = item
	}
	return result
}
func tagsByID(snapshot zenmoney.Snapshot) map[string]zenmoney.Tag {
	result := make(map[string]zenmoney.Tag, len(snapshot.Tags))
	for _, item := range snapshot.Tags {
		result[item.ID] = item
	}
	return result
}
func merchantsByID(snapshot zenmoney.Snapshot) map[string]zenmoney.Merchant {
	result := make(map[string]zenmoney.Merchant, len(snapshot.Merchants))
	for _, item := range snapshot.Merchants {
		result[item.ID] = item
	}
	return result
}
