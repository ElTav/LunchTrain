package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/ant0ine/go-json-rest/rest"
	"github.com/tbruyelle/hipchat-go/hipchat"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

/*
To-do list
---------
Start a train at a designated time
Tag people when the train leaves (message them?)
-----

90 so far
Look into moving to AWS
*/

var authKey string = ""
var roomName string = ""

var station *Station = &Station{
	Lock:       &sync.Mutex{},
	Passengers: make(map[string]*Train),
	Trains:     make(map[string]*Train),
}

var fileMutex = &sync.Mutex{}

type WebhookMessage struct {
	Item struct {
		MessageStruct struct {
			From struct {
				MentionName string `json:"mention_name"`
			}
			Message string `json:"message"`
		} `json:"message"`
	} `json:"item"`
}

type Train struct {
	Lock                *sync.Mutex
	LeavingTimer        *time.Timer
	TimeRemaining       int
	TimeRemainingTicker *time.Ticker
	Delete              chan struct{}
	MapDestination      string
	DisplayDestination  string
	PassengerSet        map[string]struct{}
}

func NewTrain(conductor string, departure int, dest string) *Train {
	timer := time.NewTimer(time.Minute * time.Duration(departure))
	ticker := time.NewTicker(time.Minute)
	trainMap := make(map[string]struct{})
	trainMap[conductor] = struct{}{}
	return &Train{
		Lock:                &sync.Mutex{},
		LeavingTimer:        timer,
		TimeRemaining:       departure,
		TimeRemainingTicker: ticker,
		Delete:              make(chan struct{}),
		MapDestination:      strings.ToLower(dest),
		DisplayDestination:  dest,
		PassengerSet:        trainMap,
	}
}

func (t *Train) NewPassenger(pass string) error {
	t.Lock.Lock()
	defer t.Lock.Unlock()
	_, ok := t.PassengerSet[pass]
	if !ok {
		t.PassengerSet[pass] = struct{}{}
		return nil
	} else {
		log.Printf("Passenger %s is already on the train\n", pass)
		return fmt.Errorf("Passenger %s is already on the train", pass)
	}
}

func (t *Train) PassengerString() string {
	t.Lock.Lock()
	defer t.Lock.Unlock()
	var buffer bytes.Buffer
	i := 0
	for v, _ := range t.PassengerSet {
		buffer.WriteString(v)
		if i != len(t.PassengerSet)-1 {
			buffer.WriteString(", ")
		}
		if i == len(t.PassengerSet)-2 {
			buffer.WriteString("and ")
		}
		i++
	}
	return buffer.String()

}

type Station struct {
	Lock       *sync.Mutex
	Passengers map[string]*Train
	Trains     map[string]*Train
}

func (s *Station) AddTrain(t *Train) error {
	s.Lock.Lock()
	defer s.Lock.Unlock()
	_, ok := s.Trains[t.MapDestination]
	if !ok {
		s.Trains[t.MapDestination] = t
		return nil
	} else {
		log.Printf("Train to %s already exists", t.DisplayDestination)
		return fmt.Errorf("Train to %s already exists", t.DisplayDestination)
	}
}

func (s *Station) DeleteTrain(dest string) error {
	s.Lock.Lock()
	defer s.Lock.Unlock()
	_, ok := s.Trains[dest]
	if ok {
		for k := range s.Trains {
			train := s.Trains[k]
			for passenger, _ := range train.PassengerSet {
				delete(s.Passengers, passenger)
			}
		}
		delete(s.Trains, dest)
		return nil
	} else {
		err := fmt.Sprintf("The train to %s doesn't exist so it can't be removed", dest)
		log.Println(err)
		return fmt.Errorf(err)
	}
}

type Message struct {
	Type        string `csv:"type"`
	Destination string `csv:"destination"`
	User        string `csv:"user"`
	RawMessage  string `csv:"message"`
	Date        string `csv:"date"`
	BaseMessage string `csv:"OG message"`
}

func NewMessage(category, destination, user, raw, base string) Message {
	return Message{
		Type:        category,
		Destination: destination,
		User:        user,
		RawMessage:  raw,
		Date:        time.Now().Format("01/02/2006"),
		BaseMessage: base,
	}
}

func PostMessage(msg Message) {
	c := hipchat.NewClient(authKey)
	if err := MessageToCSV(msg); err != nil {
		log.Println("Error writing to csv: ", err)
	}
	msgReq := &hipchat.NotificationRequest{Message: msg.RawMessage}
	_, err := c.Room.Notification(roomName, msgReq)
	if err != nil {
		panic(err)
	}
}

func MessageToCSV(msg Message) error {
	fileMutex.Lock()
	file, err := os.OpenFile("log.csv", os.O_RDWR|os.O_CREATE, 0666)
	if err != nil {
		log.Println("Error opening file: ", err)
	}
	defer file.Close()
	defer fileMutex.Unlock()
	messages := []*Message{}
	if _, err := gocsv.MarshalString(&messages); err == nil {
		if err := gocsv.UnmarshalFile(file, &messages); err != nil { // Load clients from file
			log.Println("Error unmarshaling the file")
			return err
		}
		messages := append(messages, &msg)
		if _, err := file.Seek(0, 0); err != nil { // Go to the start of the file
			log.Printf("Error seeking %v", err)
			return err
		}
		if err = gocsv.MarshalFile(&messages, file); err != nil {
			log.Println("Error writing open file ", err)
			return err
		}
	}
	return nil
}

func MonitorTrain(train *Train) {
	for {
		select {
		case <-train.Delete:
			return
		case <-train.TimeRemainingTicker.C:
			train.TimeRemaining = train.TimeRemaining - 1
			if train.TimeRemaining == 0 {
				var buffer bytes.Buffer
				start := fmt.Sprintf("The train to %v has left the station with ", train.DisplayDestination)
				buffer.WriteString(start)
				buffer.WriteString(train.PassengerString())
				buffer.WriteString(" on it!")
				msg := NewMessage("departure", train.DisplayDestination, "departure", buffer.String(), "departure")
				PostMessage(msg)
				station.DeleteTrain(train.MapDestination)
				return
			}
			if train.TimeRemaining == 1 {
				msg := NewMessage("reminder", train.DisplayDestination, "reminder", fmt.Sprintf("Reminder, the next train to %v leaves in one minute", train.DisplayDestination), "reminder")
				PostMessage(msg)
			}
		default:
		}
	}
}

func GetDestinationAndTime(start int, messageParts []string, getTime bool) (string, int, error) {
	var dest bytes.Buffer
	for i := start; i < len(messageParts); i++ {
		if getTime {
			num, err := strconv.Atoi(messageParts[i])
			if err == nil && i == len(messageParts)-1 && i != 2 {
				return dest.String(), num, nil
			}
		}
		if i > start {
			dest.WriteString(" ")
		}
		dest.WriteString(messageParts[i])
	}
	if !getTime {
		return dest.String(), 0, nil
	}
	return "", 0, fmt.Errorf("I couldn't parse your destination and/or time to departure")
}

func DitchTrain(conductor string) {
	old := station.Passengers[conductor]
	var finalMsg bytes.Buffer
	msg := fmt.Sprintf("%v has decided to ditch their train to %v.", conductor, old.DisplayDestination)
	finalMsg.WriteString(msg)
	old.Lock.Lock()
	delete(old.PassengerSet, conductor)
	old.Lock.Unlock()
	if len(old.PassengerSet) == 0 {
		msg = fmt.Sprintf(" It crashed and burned. There were no fatalities.")
		finalMsg.WriteString(msg)
		station.DeleteTrain(old.MapDestination)
		old.Delete <- struct{}{}
	}
	msgStruct := NewMessage("ditch", old.DisplayDestination, conductor, finalMsg.String(), "ditch")
	PostMessage(msgStruct)
}

func Handler(w rest.ResponseWriter, r *rest.Request) {
	var webMsg WebhookMessage
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&webMsg)
	if err != nil {
		log.Printf(err.Error())
		msg := NewMessage("error", "", "Errol", err.Error(), "error decoding json")
		PostMessage(msg)
		return
	}

	conductor := webMsg.Item.MessageStruct.From.MentionName
	baseMessage := webMsg.Item.MessageStruct.Message
	messageParts := strings.Split(baseMessage, " ")

	var msg string
	if len(messageParts) < 2 {
		msg := fmt.Sprintf("%v messed up and forgot to provide the sufficient number of params", conductor)
		msgStruct := NewMessage("insufficientParams", "", conductor, msg, baseMessage)
		PostMessage(msgStruct)
		return
	}
	if messageParts[0] != "/train" {
		msg := NewMessage("mention", "", conductor, "mention", baseMessage)
		MessageToCSV(msg)
		return
	}
	cmd := strings.ToLower(messageParts[1])
	notFound := "That train doesn't exist, please try again"
	switch cmd {
	case "help":
		msg = "Usage: /train start <destination> <#minutes> || /train join <destination> || /train passengers <destination> || /train active"
		msgStruct := NewMessage("help", "", conductor, msg, baseMessage)
		PostMessage(msgStruct)
	case "passengers":
		if len(messageParts) != 3 {
			msg := "/train passengers takes one arg"
			msgStruct := NewMessage("passengers", "", conductor, msg, baseMessage)
			PostMessage(msgStruct)
			return
		}
		dest, _, err := GetDestinationAndTime(2, messageParts, false)
		if err != nil {
			msg := NewMessage("passengersError", dest, conductor, err.Error(), baseMessage)
			PostMessage(msg)
			return
		}
		station.Lock.Lock()
		train, ok := station.Trains[strings.ToLower(dest)]
		station.Lock.Unlock()
		if !ok {
			msgStruct := NewMessage("trainNotFound", dest, conductor, notFound, baseMessage)
			PostMessage(msgStruct)
		} else {
			if len(train.PassengerSet) == 1 {
				msg = fmt.Sprintf("%v is on the train to %v", train.PassengerString(), train.DisplayDestination)
			} else {
				msg = fmt.Sprintf("%v are on the train to %v", train.PassengerString(), train.DisplayDestination)
			}
			msgStruct := NewMessage("passengers", train.DisplayDestination, conductor, msg, baseMessage)
			PostMessage(msgStruct)
		}
	case "join":
		dest, _, err := GetDestinationAndTime(2, messageParts, false)
		if err != nil {
			msgStruct := NewMessage("joinError", dest, conductor, err.Error(), baseMessage)
			PostMessage(msgStruct)
			return
		}
		station.Lock.Lock()
		train, ok := station.Trains[strings.ToLower(dest)]
		station.Lock.Unlock()
		if ok {
			station.Lock.Lock()
			_, ok := station.Passengers[conductor]
			station.Lock.Unlock()
			if ok {
				DitchTrain(conductor)
			}
			err := train.NewPassenger(conductor)
			if err == nil {
				msg = fmt.Sprintf("%s jumped on the train to %s", conductor, train.DisplayDestination)
				station.Lock.Lock()
				station.Passengers[conductor] = train
				station.Lock.Unlock()
				msgStruct := NewMessage("join", train.DisplayDestination, conductor, msg, baseMessage)
				PostMessage(msgStruct)
			} else {
				msgStruct := NewMessage("joinError", dest, conductor, err.Error(), baseMessage)
				PostMessage(msgStruct)
			}
		} else {
			msgStruct := NewMessage("joinError", dest, conductor, notFound, baseMessage)
			PostMessage(msgStruct)
		}
	case "start":
		dest, length, err := GetDestinationAndTime(2, messageParts, true)
		if err != nil {
			msgStruct := NewMessage("startError", dest, conductor, err.Error(), baseMessage)
			PostMessage(msgStruct)
			return
		}
		if length <= 0 {
			msg = fmt.Sprintf("Please specify a time greater than 0 mins")
			msgStruct := NewMessage("startError", dest, conductor, msg, baseMessage)
			PostMessage(msgStruct)
			return
		}
		station.Lock.Lock()
		_, ok := station.Trains[strings.ToLower(dest)]
		station.Lock.Unlock()
		if ok {
			msg = fmt.Sprintf("There's already a train to %v!", dest)
			msgStruct := NewMessage("startError", dest, conductor, msg, baseMessage)
			PostMessage(msgStruct)
			return
		} else {
			station.Lock.Lock()
			_, ok := station.Passengers[conductor]
			station.Lock.Unlock()
			if ok {
				DitchTrain(conductor)
			}
			train := NewTrain(conductor, length, dest)
			err = station.AddTrain(train)
			if err == nil {
				station.Lock.Lock()
				station.Passengers[conductor] = train
				station.Lock.Unlock()
				msg = fmt.Sprintf("%s has started a train to %v that leaves in %v minutes!", conductor, train.DisplayDestination, length)
				msgStruct := NewMessage("startTrain", dest, conductor, msg, baseMessage)
				PostMessage(msgStruct)
				go MonitorTrain(train)
			} else {
				msgStruct := NewMessage("startError", dest, conductor, err.Error(), baseMessage)
				PostMessage(msgStruct)
				return
			}
		}
	case "active":
		if len(messageParts) != 2 {
			msgStruct := NewMessage("activeError", "", conductor, "/train active takes no additional args", baseMessage)
			PostMessage(msgStruct)
			return
		}
		if len(station.Trains) == 0 {
			msg = fmt.Sprintf("There are currently no active trains")
			msgStruct := NewMessage("activeError", "", conductor, msg, baseMessage)
			PostMessage(msgStruct)
		} else {
			var finalMsg bytes.Buffer
			finalMsg.WriteString("There are trains to: ")
			i := 0
			station.Lock.Lock()
			for _, v := range station.Trains {
				if len(station.Trains) == 1 {
					msg = fmt.Sprintf("There is currently a train to %v in %v mins (with %v on it)", v.DisplayDestination, v.TimeRemaining, v.PassengerString())
					msgStruct := NewMessage("active", v.DisplayDestination, conductor, msg, baseMessage)
					PostMessage(msgStruct)
					return
				} else {
					finalMsg.WriteString(fmt.Sprintf("%v in %v mins (with %v on it)", v.DisplayDestination, v.TimeRemaining, v.PassengerString()))
				}
				if i != len(station.Trains)-1 {
					finalMsg.WriteString(", ")
				}
				if i == len(station.Trains)-2 {
					finalMsg.WriteString("and ")
				}
				i++
			}
			station.Lock.Unlock()
			msgStruct := NewMessage("active", "", conductor, finalMsg.String(), baseMessage)
			PostMessage(msgStruct)
		}
	default:
		msg := "Your command could not be found, please view the help message (/train help) for more details"
		msgStruct := NewMessage("malformed", "", conductor, msg, baseMessage)
		PostMessage(msgStruct)
	}
}

func ValidityHandler(w rest.ResponseWriter, r *rest.Request) {
	str := "Everything is OK!"
	w.WriteJson(&str)
}

func main() {
	api := rest.NewApi()
	api.Use(rest.DefaultDevStack...)

	router, err := rest.MakeRouter(
		rest.Get("/", ValidityHandler),
		rest.Post("/train", Handler),
	)

	api.SetApp(router)
	ip := os.Getenv("OPENSHIFT_GO_IP")
	port := os.Getenv("OPENSHIFT_GO_PORT")
	if port == "" {
		port = "8080"
	}
	bind := fmt.Sprintf("%s:%s", ip, port)
	err = http.ListenAndServe(bind, api.MakeHandler())
	if err != nil {
		log.Println(err)
	}
}
