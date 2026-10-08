package app

import "net/http"

func (a *App) setUnderstood(w http.ResponseWriter, r *http.Request) {
	id, ok := a.id(w, r)
	if !ok {
		return
	}
	value := r.PostFormValue("understood")
	if value != "true" && value != "false" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "理解済みの状態を選び直してください。"})
		return
	}
	understood := value == "true"
	if err := a.db.SetUnderstood(r.Context(), session(r).User.ID, id, understood); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"understood": understood})
}
