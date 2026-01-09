package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type App struct {
	db      *sql.DB
	tpl     *template.Template
	secret  string
	signup  bool
}

func main() {
	dbPath := getenv("DB_PATH", "/data/app.db")
	secret := getenv("COOKIE_SECRET", "change-me")
	allowSignup := strings.ToLower(getenv("ALLOW_SIGNUP", "false")) == "true"

	db, err := sql.Open("sqlite", dbPath)
	if err != nil { log.Fatal(err) }
	defer db.Close()

	if err := migrate(db); err != nil { log.Fatal(err) }

	app := &App{
		db:     db,
		tpl:    template.Must(template.ParseGlob("templates/*.html")),
		secret: secret,
		signup: allowSignup,
	}

	// create admin if empty and env provided
	app.bootstrapAdmin()

	mux := http.NewServeMux()
	mux.HandleFunc("/login", app.loginHandler)
	mux.HandleFunc("/logout", app.logoutHandler)
	mux.HandleFunc("/signup", app.signupHandler)

	mux.HandleFunc("/", app.requireAuth(app.dashboardHandler))
	mux.HandleFunc("/watch/add", app.requireAuth(app.addWatchHandler))
	mux.HandleFunc("/watch/delete", app.requireAuth(app.deleteWatchHandler))

	mux.HandleFunc("/telegram", app.requireAuth(app.telegramHandler))
	mux.HandleFunc("/telegram/save", app.requireAuth(app.telegramSaveHandler))

	addr := ":8080"
	log.Println("Web listening on", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func getenv(k, def string) string {
	v := os.Getenv(k)
	if v == "" { return def }
	return v
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT UNIQUE NOT NULL,
			pass_hash TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS sessions(
			token TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS settings(
			id INTEGER PRIMARY KEY CHECK(id=1),
			tg_token TEXT,
			tg_chat_id TEXT
		);`,
		`INSERT OR IGNORE INTO settings(id, tg_token, tg_chat_id) VALUES(1, '', '');`,
		`CREATE TABLE IF NOT EXISTS watches(
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT NOT NULL,
			model TEXT NOT NULL,
			count_regex TEXT,
			instock_regex TEXT,
			oos_regex TEXT,
			window INTEGER NOT NULL DEFAULT 2500,
			enabled INTEGER NOT NULL DEFAULT 1,

			last_status TEXT,
			last_available INTEGER,
			last_checked INTEGER,
			last_notified INTEGER
		);`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil { return err }
	}
	return nil
}

func (a *App) bootstrapAdmin() {
	adminUser := os.Getenv("ADMIN_USER")
	adminPass := os.Getenv("ADMIN_PASS")
	if adminUser == "" || adminPass == "" {
		return
	}
	var cnt int
	_ = a.db.QueryRow(`SELECT COUNT(*) FROM users;`).Scan(&cnt)
	if cnt > 0 { return }

	hash, _ := bcrypt.GenerateFromPassword([]byte(adminPass), bcrypt.DefaultCost)
	_, err := a.db.Exec(`INSERT INTO users(username, pass_hash, created_at) VALUES(?,?,?)`,
		adminUser, string(hash), time.Now().Unix())
	if err != nil {
		log.Println("bootstrap admin failed:", err)
	} else {
		log.Println("bootstrap admin created:", adminUser)
	}
}

func (a *App) setSession(w http.ResponseWriter, userID int64) error {
	token := randHex(32)
	exp := time.Now().Add(7 * 24 * time.Hour).Unix()
	_, err := a.db.Exec(`INSERT INTO sessions(token, user_id, expires_at) VALUES(?,?,?)`, token, userID, exp)
	if err != nil { return err }

	c := &http.Cookie{
		Name:     "sess",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, c)
	return nil
}

func (a *App) clearSession(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("sess")
	if err == nil && c.Value != "" {
		_, _ = a.db.Exec(`DELETE FROM sessions WHERE token=?`, c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "sess", Value: "", Path: "/", MaxAge: -1})
}

func (a *App) currentUserID(r *http.Request) (int64, bool) {
	c, err := r.Cookie("sess")
	if err != nil || c.Value == "" { return 0, false }

	var uid int64
	var exp int64
	err = a.db.QueryRow(`SELECT user_id, expires_at FROM sessions WHERE token=?`, c.Value).Scan(&uid, &exp)
	if err != nil { return 0, false }
	if time.Now().Unix() > exp {
		return 0, false
	}
	return uid, true
}

func (a *App) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := a.currentUserID(r)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next(w, r)
	}
}

func (a *App) loginHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		_ = a.tpl.ExecuteTemplate(w, "login.html", map[string]any{"Signup": a.signup})
		return
	case "POST":
		_ = r.ParseForm()
		u := r.FormValue("username")
		p := r.FormValue("password")
		var id int64
		var hash string
		err := a.db.QueryRow(`SELECT id, pass_hash FROM users WHERE username=?`, u).Scan(&id, &hash)
		if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(p)) != nil {
			_ = a.tpl.ExecuteTemplate(w, "login.html", map[string]any{"Error": "用户名或密码错误", "Signup": a.signup})
			return
		}
		if err := a.setSession(w, id); err != nil {
			http.Error(w, err.Error(), 500); return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func (a *App) signupHandler(w http.ResponseWriter, r *http.Request) {
	if !a.signup {
		http.Error(w, "signup disabled", 403); return
	}
	switch r.Method {
	case "GET":
		_ = a.tpl.ExecuteTemplate(w, "login.html", map[string]any{"Signup": true, "ShowSignup": true})
	case "POST":
		_ = r.ParseForm()
		u := r.FormValue("username")
		p := r.FormValue("password")
		if len(u) < 3 || len(p) < 6 {
			_ = a.tpl.ExecuteTemplate(w, "login.html", map[string]any{"Error": "用户名>=3，密码>=6", "Signup": true, "ShowSignup": true})
			return
		}
		hash, _ := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
		res, err := a.db.Exec(`INSERT INTO users(username, pass_hash, created_at) VALUES(?,?,?)`, u, string(hash), time.Now().Unix())
		if err != nil {
			_ = a.tpl.ExecuteTemplate(w, "login.html", map[string]any{"Error": "注册失败(用户名可能已存在)", "Signup": true, "ShowSignup": true})
			return
		}
		id, _ := res.LastInsertId()
		_ = a.setSession(w, id)
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

func (a *App) logoutHandler(w http.ResponseWriter, r *http.Request) {
	a.clearSession(w, r)
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (a *App) dashboardHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := a.db.Query(`SELECT id,url,model,enabled,last_status,last_available,last_checked FROM watches ORDER BY id DESC`)
	if err != nil { http.Error(w, err.Error(), 500); return }
	defer rows.Close()

	type Watch struct {
		ID int64
		URL string
		Model string
		Enabled int
		LastStatus string
		LastAvailable sql.NullInt64
		LastChecked sql.NullInt64
	}
	var list []Watch
	for rows.Next() {
		var wch Watch
		_ = rows.Scan(&wch.ID, &wch.URL, &wch.Model, &wch.Enabled, &wch.LastStatus, &wch.LastAvailable, &wch.LastChecked)
		list = append(list, wch)
	}
	_ = a.tpl.ExecuteTemplate(w, "dashboard.html", map[string]any{"Watches": list})
}

func (a *App) addWatchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, "method", 405); return }
	_ = r.ParseForm()
	url := strings.TrimSpace(r.FormValue("url"))
	model := strings.TrimSpace(r.FormValue("model"))
	countRegex := strings.TrimSpace(r.FormValue("count_regex"))
	instockRegex := strings.TrimSpace(r.FormValue("instock_regex"))
	oosRegex := strings.TrimSpace(r.FormValue("oos_regex"))
	window, _ := strconv.Atoi(r.FormValue("window"))
	if window <= 0 { window = 2500 }
	if url == "" || model == "" {
		http.Redirect(w, r, "/", http.StatusFound); return
	}
	_, err := a.db.Exec(`INSERT INTO watches(url,model,count_regex,instock_regex,oos_regex,window,enabled) VALUES(?,?,?,?,?,?,1)`,
		url, model, countRegex, instockRegex, oosRegex, window)
	if err != nil { http.Error(w, err.Error(), 500); return }
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *App) deleteWatchHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, "method", 405); return }
	_ = r.ParseForm()
	id := r.FormValue("id")
	if id != "" {
		_, _ = a.db.Exec(`DELETE FROM watches WHERE id=?`, id)
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *App) telegramHandler(w http.ResponseWriter, r *http.Request) {
	var token, chat string
	_ = a.db.QueryRow(`SELECT tg_token, tg_chat_id FROM settings WHERE id=1`).Scan(&token, &chat)
	_ = a.tpl.ExecuteTemplate(w, "telegram.html", map[string]any{"Token": token, "Chat": chat})
}

func (a *App) telegramSaveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, "method", 405); return }
	_ = r.ParseForm()
	token := strings.TrimSpace(r.FormValue("tg_token"))
	chat := strings.TrimSpace(r.FormValue("tg_chat_id"))
	_, err := a.db.Exec(`UPDATE settings SET tg_token=?, tg_chat_id=? WHERE id=1`, token, chat)
	if err != nil { http.Error(w, err.Error(), 500); return }
	http.Redirect(w, r, "/telegram", http.StatusFound)
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
