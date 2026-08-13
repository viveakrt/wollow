package moneyapi

import (
	"net/http"
	"wollow/backend/internal/platform/httpx"

	"wollow/backend/internal/money/models"
)

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(`SELECT id, name, type, icon, color, sort_order FROM categories ORDER BY sort_order, name`)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	defer rows.Close()

	cats := []models.Category{}
	for rows.Next() {
		var c models.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.Icon, &c.Color, &c.SortOrder); err != nil {
			httpx.WriteError(w, 500, err.Error())
			return
		}
		cats = append(cats, c)
	}
	httpx.WriteJSON(w, 200, cats)
}

func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var c models.Category
	if err := httpx.DecodeJSON(r, &c); err != nil {
		httpx.WriteError(w, 400, "invalid body")
		return
	}
	if c.Name == "" {
		httpx.WriteError(w, 400, "name is required")
		return
	}
	if c.Type == "" {
		c.Type = "expense"
	}
	if c.Color == "" {
		c.Color = "#8b5cf6"
	}
	res, err := s.DB.Exec(`INSERT INTO categories (name, type, icon, color, sort_order) VALUES (?, ?, ?, ?, ?)`,
		c.Name, c.Type, c.Icon, c.Color, c.SortOrder)
	if err != nil {
		httpx.WriteError(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	c.ID = id
	httpx.WriteJSON(w, 201, c)
}
