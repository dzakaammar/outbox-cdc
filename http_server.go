package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

var appTimeout = 10 * time.Second

func runHTTPServer() {
	dbConn, err := pgx.Connect(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /users", handleCreateUser(dbConn))

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt)
	stopCh := make(chan error, 1)

	srv := http.Server{
		Addr:         ":8080",
		Handler:      mux,
		WriteTimeout: appTimeout,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			stopCh <- err
		}
	}()

	go func() {
		<-signalCh
		ctx, cancel := context.WithTimeout(context.Background(), appTimeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			stopCh <- err
		}
	}()

	<-stopCh
	log.Println("app stopped")
}

type createUserRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Address string `json:"address"`
	Phone   string `json:"phone"`
}

func handleCreateUser(dbConn *pgx.Conn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		err := storeUser(r.Context(), dbConn, req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func storeUser(ctx context.Context, dbConn *pgx.Conn, req createUserRequest) error {
	tx, err := dbConn.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var id int
	err = tx.QueryRow(ctx, `INSERT INTO "users" (name, email, address, phone) VALUES (@name, @email, @address, @phone) RETURNING id;`, pgx.NamedArgs{
		"name":    req.Name,
		"email":   req.Email,
		"address": req.Address,
		"phone":   req.Phone,
	}).Scan(&id)
	if err != nil {
		return err
	}

	data, _ := json.Marshal(req)

	var outboxID int
	err = tx.QueryRow(ctx, `INSERT INTO "outbox" (event_name, object_name, object_id, data) VALUES (@eventName, @objectName, @objectID, @data) RETURNING id;`, pgx.NamedArgs{
		"eventName":  userCreated,
		"objectName": "user",
		"objectID":   strconv.Itoa(id),
		"data":       data,
	}).Scan(&outboxID)
	if err != nil {
		return err
	}

	log.Println("outbox has successfully created with id : " + strconv.Itoa(outboxID))

	return tx.Commit(ctx)
}
