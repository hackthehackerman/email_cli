package main

import (
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"gopkg.in/yaml.v3"
)

type Credential struct {
	Email    string `yaml:"email"`
	Password string `yaml:"password"`
}

type IMAP struct {
	Address string `yaml:"address"`
}

type Config struct {
	IMAP        IMAP         `yaml:"imap"`
	Credentials []Credential `yaml:"credentials"`
}

func main() {
	file, err := os.Open("config.yaml")
	if err != nil {
		panic(err)
	}
	defer file.Close()
	var config Config
	decoder := yaml.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		panic(err)
	}

	// Create a channel to handle signals for graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	for _, cred := range config.Credentials {
		go func(credential Credential, address string) {
			// Connect to the IMAP server
			c, err := client.DialTLS(address, nil)
			if err != nil {
				log.Fatalf("Failed to connect: %v", err)
			}
			defer c.Logout()

			// Login to the email account
			if err := c.Login(credential.Email, credential.Password); err != nil {
				log.Fatalf("Login failed: %v", err)
			}

			var lastSeenSeqNumber uint32

			for range time.Tick(5 * time.Second) {
				log.Println("Polling. last seen seq number:", lastSeenSeqNumber)
				// Select the INBOX mailbox
				mbox, err := c.Select("INBOX", false)
				if err != nil {
					log.Fatalf("Select mailbox failed: %v", err)
				}
				if lastSeenSeqNumber >= mbox.Messages {
					continue
				}
				// Set up a search criteria to fetch all emails
				seqset := new(imap.SeqSet)
				// seqset.Add(fmt.Sprintf("%d:*", lastSeenUID))
				seqset.AddRange(lastSeenSeqNumber+1, mbox.Messages)
				section := &imap.BodySectionName{}

				// Fetch the emails
				messages := make(chan *imap.Message, 10)
				go func() {
					if err := c.Fetch(seqset, []imap.FetchItem{section.FetchItem()}, messages); err != nil {
						log.Fatalf("Fetch failed: %v", err)
					}
				}()

				for msg := range messages {
					lastSeenSeqNumber = maxInt(lastSeenSeqNumber, msg.SeqNum)
					r := msg.GetBody(section)
					if r == nil {
						log.Fatal("Server didn't returned message body")
					}

					m, err := mail.ReadMessage(r)
					if err != nil {
						log.Fatal(err)
					}

					header := m.Header

					if err != nil {
						log.Fatal(err)
					}
					text, err := Text(m)
					if err != nil {
						log.Fatal(err)
					}
					WriteMail(header, text)
				}
			}

		}(cred, config.IMAP.Address)
	}
	<-quit
}

func maxInt(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

func Text(m *mail.Message) (text string, err error) {
	var sb strings.Builder

	var processMultipart func(mr *multipart.Reader)
	processMultipart = func(mr *multipart.Reader) {
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break // End of the email
			}
			if err != nil {
				panic(err)
			}

			partMediaType, partParams, err := mime.ParseMediaType(p.Header.Get("Content-Type"))
			if err != nil {
				panic(err)
			}

			// Check if the part is text
			if strings.HasPrefix(partMediaType, "text/") {
				// Read the part
				partBytes, err := io.ReadAll(p)
				if err != nil {
					panic(err)
				}

				// The part is text, print it
				// fmt.Println(p.Header.Get("Content-Type"))
				// fmt.Println(string(partBytes))
				sb.Write(partBytes)
				p.Close()
			} else if strings.HasPrefix(partMediaType, "multipart/") {
				nestedMR := multipart.NewReader(p, partParams["boundary"])
				processMultipart(nestedMR)
			}

			// Close the part
			p.Close()
		}
	}

	mediaType, params, err := mime.ParseMediaType(m.Header.Get("Content-Type"))
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		processMultipart(multipart.NewReader(m.Body, params["boundary"]))
	}
	return sb.String(), nil
}

func WriteMail(header mail.Header, text string) {
	log.Println("Date:", header.Get("Date"))
	log.Println("From:", header.Get("From"))
	log.Println("To:", header.Get("To"))
	log.Println("Subject:", header.Get("Subject"))
	log.Println("TextContent:", text)
}
