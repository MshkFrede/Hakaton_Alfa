// Package domain содержит модель кредитной заявки: статусы, машину состояний,
// каталог продуктов и общие типы. Пакет не зависит от БД и HTTP.
package domain

import "fmt"

// Status — состояние кредитной заявки.
type Status string

const (
	StatusDraft            Status = "DRAFT"              // черновик, клиент заполняет
	StatusSubmitted        Status = "SUBMITTED"          // подана, ждёт обработки
	StatusEnrichment       Status = "ENRICHMENT"         // запросы во внешние источники (ЕГРЮЛ, БКИ, ФНС)
	StatusScoring          Status = "SCORING"            // скоринг и кредитная политика
	StatusManualReview     Status = "MANUAL_REVIEW"      // ручной разбор андеррайтером
	StatusDocsRequested    Status = "DOCS_REQUESTED"     // банк запросил документы у клиента
	StatusApproved         Status = "APPROVED"           // одобрено на запрошенных условиях
	StatusCounterOffer     Status = "COUNTER_OFFER"      // одобрено на изменённых условиях
	StatusSigning          Status = "SIGNING"            // клиент принял условия, подписание
	StatusSigned           Status = "SIGNED"             // договор подписан, идёт выдача
	StatusIssued           Status = "ISSUED"             // средства выданы (финал)
	StatusRejected         Status = "REJECTED"           // отказ банка (финал)
	StatusDeclinedByClient Status = "DECLINED_BY_CLIENT" // клиент отказался от предложения (финал)
	StatusCancelled        Status = "CANCELLED"          // клиент отозвал заявку (финал)
)

// AllStatuses — все статусы в порядке жизненного цикла.
var AllStatuses = []Status{
	StatusDraft, StatusSubmitted, StatusEnrichment, StatusScoring, StatusManualReview,
	StatusDocsRequested, StatusApproved, StatusCounterOffer, StatusSigning, StatusSigned,
	StatusIssued, StatusRejected, StatusDeclinedByClient, StatusCancelled,
}

// IsFinal — заявка в конечном состоянии.
func (s Status) IsFinal() bool {
	switch s {
	case StatusIssued, StatusRejected, StatusDeclinedByClient, StatusCancelled:
		return true
	}
	return false
}

// IsValid проверяет, что строка — известный статус.
func (s Status) IsValid() bool {
	for _, st := range AllStatuses {
		if st == s {
			return true
		}
	}
	return false
}

// ActorType — кто инициирует переход.
type ActorType string

const (
	ActorClient      ActorType = "CLIENT"
	ActorUnderwriter ActorType = "UNDERWRITER"
	ActorSystem      ActorType = "SYSTEM"
)

// transitions — единственный источник правды о допустимых переходах.
// Ключ: из какого статуса; значение: в какой статус и какие акторы могут его выполнить.
// Эта же таблица отрисована в docs/state-diagram.md.
var transitions = map[Status]map[Status][]ActorType{
	StatusDraft: {
		StatusSubmitted: {ActorClient},
		StatusCancelled: {ActorClient},
	},
	StatusSubmitted: {
		StatusEnrichment: {ActorSystem},
	},
	StatusEnrichment: {
		StatusScoring: {ActorSystem},
	},
	StatusScoring: {
		StatusApproved:     {ActorSystem},
		StatusCounterOffer: {ActorSystem},
		StatusManualReview: {ActorSystem},
		StatusRejected:     {ActorSystem},
	},
	StatusManualReview: {
		StatusApproved:      {ActorUnderwriter},
		StatusCounterOffer:  {ActorUnderwriter},
		StatusDocsRequested: {ActorUnderwriter},
		StatusRejected:      {ActorUnderwriter},
	},
	StatusDocsRequested: {
		StatusManualReview: {ActorClient},
		StatusCancelled:    {ActorClient},
	},
	StatusApproved: {
		StatusSigning:          {ActorClient},
		StatusDeclinedByClient: {ActorClient},
	},
	StatusCounterOffer: {
		StatusSigning:          {ActorClient},
		StatusDeclinedByClient: {ActorClient},
	},
	StatusSigning: {
		StatusSigned:           {ActorClient},
		StatusDeclinedByClient: {ActorClient},
	},
	StatusSigned: {
		StatusIssued: {ActorSystem},
	},
}

// ErrTransitionNotAllowed возвращается при попытке недопустимого перехода.
type ErrTransitionNotAllowed struct {
	From, To Status
	Actor    ActorType
}

func (e ErrTransitionNotAllowed) Error() string {
	return fmt.Sprintf("переход %s → %s недоступен для %s", e.From, e.To, e.Actor)
}

// CanTransition проверяет переход по таблице.
func CanTransition(from, to Status, actor ActorType) error {
	if tos, ok := transitions[from]; ok {
		for _, a := range tos[to] {
			if a == actor {
				return nil
			}
		}
	}
	return ErrTransitionNotAllowed{From: from, To: to, Actor: actor}
}

// TransitionRow — строка таблицы переходов (для API и документации).
type TransitionRow struct {
	From   Status      `json:"from"`
	To     Status      `json:"to"`
	Actors []ActorType `json:"actors"`
}

// TransitionTable возвращает таблицу переходов в стабильном порядке.
func TransitionTable() []TransitionRow {
	var rows []TransitionRow
	for _, from := range AllStatuses {
		for _, to := range AllStatuses {
			if actors, ok := transitions[from][to]; ok {
				rows = append(rows, TransitionRow{From: from, To: to, Actors: actors})
			}
		}
	}
	return rows
}

// StatusTitle — человекочитаемое название статуса для клиента.
var StatusTitle = map[Status]string{
	StatusDraft:            "Черновик",
	StatusSubmitted:        "Заявка принята",
	StatusEnrichment:       "Проверяем данные",
	StatusScoring:          "Оцениваем заявку",
	StatusManualReview:     "На рассмотрении у специалиста",
	StatusDocsRequested:    "Нужны документы",
	StatusApproved:         "Одобрено",
	StatusCounterOffer:     "Одобрено на других условиях",
	StatusSigning:          "Подписание договора",
	StatusSigned:           "Договор подписан, выдаём средства",
	StatusIssued:           "Средства выданы",
	StatusRejected:         "Отказ",
	StatusDeclinedByClient: "Вы отказались от предложения",
	StatusCancelled:        "Заявка отозвана",
}
