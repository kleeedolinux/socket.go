package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kleeedolinux/socket.go/socket"
)


type ChatMessage struct {
	Username string `json:"username"`
	Message  string `json:"message"`
	Time     int64  `json:"time"`
}

func main() {
	
	port := flag.Int("port", 8080, "HTTP server port")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lshortfile)

	
	server := socket.NewServer(
		socket.WithPingInterval(25*time.Second),
		socket.WithPingTimeout(5*time.Second),
	)

	
	server.HandleFunc(socket.Event("chat"), func(s socket.Socket, data interface{}) {
		
		var message ChatMessage

		log.Printf("Received chat message from socket %s: %v", s.ID(), data)

		if jsonData, err := json.Marshal(data); err == nil {
			if err := json.Unmarshal(jsonData, &message); err != nil {
				log.Printf("Error unmarshaling chat message: %v", err)
				return
			}
		} else {
			log.Printf("Error marshaling chat message: %v", err)
			return
		}

		
		if message.Time == 0 {
			message.Time = time.Now().Unix()
		}

		log.Printf("Chat message from %s: %s", message.Username, message.Message)

		
		log.Printf("Broadcasting chat message to all clients")
		server.Broadcast(socket.Event("chat"), message)
	})

	
	server.HandleFunc(socket.Event("ping"), func(s socket.Socket, data interface{}) {
		
		s.Send(socket.Event("ping"), data)
	})

	
	server.HandleFunc(socket.EventConnect, func(s socket.Socket, _ interface{}) {
		log.Printf("Client connected: %s", s.ID())

		
		server.Broadcast(socket.Event("system"), map[string]interface{}{
			"message": "A user has joined the chat",
			"online":  server.Count(),
		})

		
		s.Send(socket.Event("system"), map[string]interface{}{
			"message": fmt.Sprintf("Welcome! You are connected with ID: %s", s.ID()),
			"online":  server.Count(),
		})
	})

	
	server.HandleFunc(socket.EventDisconnect, func(s socket.Socket, _ interface{}) {
		log.Printf("Client disconnected: %s", s.ID())

		
		server.Broadcast(socket.Event("system"), map[string]interface{}{
			"message": "A user has left the chat",
			"online":  server.Count(),
		})
	})

	
	mux := http.NewServeMux()

	
	mux.HandleFunc("/socket", server.HandleHTTP)
	mux.HandleFunc("/socket/", server.HandleHTTP)

	
	mux.HandleFunc("/", serveIndexFile)

	
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: mux,
	}

	
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt, syscall.SIGTERM)
		<-sigint

		log.Println("Shutting down server...")
		httpServer.Close()
		os.Exit(0)
	}()

	log.Printf("Chat server started on port %d", *port)
	log.Printf("Open http://localhost:%d in your browser", *port)

	
	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
}

func serveIndexFile(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	
	indexPath := "./examples/chat/index.html"

	
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		
		indexPath = "./index.html"
		if _, err := os.Stat(indexPath); os.IsNotExist(err) {
			
			http.Error(w, "Could not find index.html", http.StatusInternalServerError)
			return
		}
	}

	
	http.ServeFile(w, r, indexPath)
}
