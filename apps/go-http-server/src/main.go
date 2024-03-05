package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// User represents a simple user model in our application
type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Repo acts as a simple in-memory database
type Repo struct {
	mtx    sync.RWMutex
	users  map[int]User
	nextID int
}

func NewRepo() *Repo {
	return &Repo{
		users:  make(map[int]User),
		nextID: 1,
	}
}

func (r *Repo) Get() (map[int]User, error) {
	r.mtx.RLock()
	defer r.mtx.RUnlock()
	return r.users, nil
}

func (r *Repo) Find(id int) (User, bool) {
	r.mtx.RLock()
	defer r.mtx.RUnlock()
	user, ok := r.users[id]
	return user, ok
}

func (r *Repo) Save(user User) User {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	user.ID = r.nextID
	r.users[user.ID] = user
	r.nextID++
	return user
}

func (r *Repo) Delete(id int) {
	r.mtx.Lock()
	defer r.mtx.Unlock()
	delete(r.users, id)
}

var repo = NewRepo()

func ListUserHandler(w http.ResponseWriter, r *http.Request) {
	users, err := repo.Get()
	if err != nil {
		http.Error(w, "failed to get users", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(users)
}

func GetUserHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	user, ok := repo.Find(id)
	if !ok {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(user)
}

func CreateUserHandler(w http.ResponseWriter, r *http.Request) {
	var user User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	user = repo.Save(user)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)

	var meter = otel.Meter("go-http-server-e")
	registeredUsersCount, err := meter.Int64Counter(
		"registeredUsers.count",
		metric.WithDescription("Number of registered users."),
		metric.WithUnit("{call}"),
	)
	if err != nil {
		fmt.Printf("error otel: %s", err.Error())
	}

	registeredUsersCount.Add(r.Context(), 1)

	json.NewEncoder(w).Encode(user)
}

func DeleteUserHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(mux.Vars(r)["id"])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	repo.Delete(id)
	w.WriteHeader(http.StatusNoContent)
}

func ContentTypeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accept := r.Header.Get("Accept")
		if accept != "application/json" {
			fmt.Println("accept header is not application/json")
		}

		w.Header().Set("Content-Type", "application/json")

		next.ServeHTTP(w, r)
	})
}

func main() {
	// Handle SIGINT (CTRL+C) gracefully.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Set up OpenTelemetry.
	otelShutdown, err := setupOTelSDK(ctx)
	if err != nil {
		log.Fatal(err.Error())
	}
	// Handle shutdown properly so nothing leaks.
	defer func() {
		err = errors.Join(err, otelShutdown(context.Background()))
	}()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	router := mux.NewRouter()
	router.Use(ContentTypeMiddleware)
	router.Handle("/metrics", promhttp.Handler())
	router.HandleFunc("/users", ListUserHandler).Methods("GET")
	router.HandleFunc("/users/{id:[0-9]+}", GetUserHandler).Methods("GET")
	router.HandleFunc("/users", CreateUserHandler).Methods("POST")
	router.HandleFunc("/users/{id:[0-9]+}", DeleteUserHandler).Methods("DELETE")

	srv := &http.Server{
		Addr:         ":8080",
		BaseContext:  func(_ net.Listener) context.Context { return ctx },
		ReadTimeout:  time.Second,
		WriteTimeout: 10 * time.Second,
		Handler:      router,
	}
	srvErr := make(chan error, 1)
	go func() {
		fmt.Println("running server")
		srvErr <- http.ListenAndServe(fmt.Sprintf(":%s", port), router)
	}()

	// Wait for interruption.
	select {
	case err = <-srvErr:
		// Error when starting HTTP server.
		return
	case <-ctx.Done():
		// Wait for first CTRL+C.
		// Stop receiving signal notifications as soon as possible.
		stop()
	}

	// When Shutdown is called, ListenAndServe immediately returns ErrServerClosed.
	err = srv.Shutdown(context.Background())
}
