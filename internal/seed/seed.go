// Package seed создаёт синтетические демо-данные: компании и учётные записи.
// Выполняется при первом запуске (пустая БД), отключается SEED_DEMO_DATA=false.
package seed

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"credit-conveyor/internal/auth"
	"credit-conveyor/internal/domain"
	"credit-conveyor/internal/mocks"
	"credit-conveyor/internal/store"
)

const DemoPassword = "demo1234"

type demoUser struct {
	Email, Name, Position string
	Role                  domain.Role
	INN                   string // пусто — без компании (онбординг)
}

// Users — демо-учётки. Описаны в README и docs/test-scenarios.md.
var Users = []demoUser{
	{"anna@romashka.demo", "Иванова Анна Сергеевна", "Генеральный директор", domain.RoleClient, mocks.INNRomashka},
	{"dmitry@vector.demo", "Кузнецов Дмитрий Олегович", "Генеральный директор", domain.RoleClient, mocks.INNVector},
	{"igor@severov.demo", "Северов Игорь Павлович", "Индивидуальный предприниматель", domain.RoleClient, mocks.INNSeverov},
	{"sergey@gorizont.demo", "Морозов Сергей Анатольевич", "Генеральный директор", domain.RoleClient, mocks.INNGorizont},
	{"irina@tuman.demo", "Зайцева Ирина Валерьевна", "Главный бухгалтер", domain.RoleClient, mocks.INNTuman},
	{"alexey@ladoga.demo", "Смирнов Алексей Викторович", "Генеральный директор", domain.RoleClient, ""},
	{"olga@bank.demo", "Петрова Ольга Николаевна", "Кредитный аналитик", domain.RoleUnderwriter, ""},
}

func Run(ctx context.Context, st *store.Store) (bool, error) {
	n, err := st.CountUsers(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	hash, err := auth.HashPassword(DemoPassword)
	if err != nil {
		return false, err
	}
	return true, st.Tx(ctx, func(tx *sql.Tx) error {
		for _, u := range Users {
			var companyID *string
			if u.INN != "" {
				p, _ := mocks.LookupProfile(u.INN)
				c, err := st.UpsertCompany(ctx, tx, p.EGRUL)
				if err != nil {
					return err
				}
				companyID = &c.ID
			}
			if err := st.CreateUser(ctx, tx, domain.User{ID: uuid.NewString(), Email: u.Email, PasswordHash: hash,
				FullName: u.Name, Position: u.Position, Role: u.Role, CompanyID: companyID}); err != nil {
				return err
			}
		}
		return nil
	})
}
