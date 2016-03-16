package main

import (
    "github.com/ant0ine/go-json-rest/rest"
    "github.com/tbruyelle/hipchat-go/hipchat"
    "bytes"
    "encoding/json"
    "fmt"
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
Trains active
Manually depart a train
Derail a train
Start a train at a designated time
Tag people when the train leaves
Propose a train that starts if enough people join -> X
-----
Keep track of usage statistics
Look into making your own log to log things

53 so far
Look into moving to AWS
*/

var authKey string = ""
var roomName string = ""

var station *Station = &Station{
	Lock: &sync.Mutex{},
	Trains: make(map[string]*Train),
}

type WebhookMessage struct {
    Item struct {
        MessageStruct struct {
        	From struct {
        		MentionName string `json:"mention_name"`
        	}
        	Message string `json:"message"`
        } `json:"message"`
        
    }  `json:"item"`
}

type Train struct {
	Lock *sync.Mutex
	LeavingTimer *time.Timer
	ReminderTimer *time.Timer
	MapDestination string
	DisplayDestination string
	Passengers []string
	PassengerSet map[string]struct{}
}

func NewTrain(conductor string, departure int, dest string) *Train {
	timer := time.NewTimer(time.Minute * time.Duration(departure))
	timer2 := time.NewTimer(time.Minute * time.Duration(departure - 1))	
	users := []string{conductor}
	trainMap := make(map[string]struct{})
	trainMap[conductor] = struct{}{}
	return &Train{
		Lock: &sync.Mutex{},
		LeavingTimer: timer,
		ReminderTimer: timer2,
		MapDestination: strings.ToLower(dest),
		DisplayDestination: dest,
		Passengers: users,
		PassengerSet: trainMap,
	}	
}
 
func (t *Train) NewPassenger(pass string) error {
	t.Lock.Lock()
	defer t.Lock.Unlock()
	_, ok := t.PassengerSet[pass]
	if !ok {
		t.PassengerSet[pass] = struct{}{}
		t.Passengers = append(t.Passengers, pass)
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
	for i, v := range t.Passengers {
	   buffer.WriteString(v)
	   if i != len(t.Passengers) - 1 {
	    	buffer.WriteString(", ")
	    }
	   if i == len(t.Passengers) - 2 {
	    	buffer.WriteString("and ")
	   }
	}
	return buffer.String()
	 
}

type Station struct {
	Lock *sync.Mutex
	Trains map[string]*Train
}

func (s *Station) AddTrain(t *Train) error {
	s.Lock.Lock()
	defer s.Lock.Unlock()
	_, ok := s.Trains[t.MapDestination]
	if !ok {
		s.Trains[t.MapDestination] = t
		return nil
	} else {
		return fmt.Errorf("Train to %s already exists", t.DisplayDestination)
	}
}

func (s *Station) DeleteTrain(dest string) error {
	s.Lock.Lock()
	defer s.Lock.Unlock()
	_, ok := s.Trains[dest]
	if ok {
		 delete(s.Trains, dest)
		 return nil
	} else {
		return fmt.Errorf("The train to %s doesn't exist so it can't be removed", dest)
	}
}

func PostMessage(msg string) {
	c := hipchat.NewClient(authKey)
	msgReq := &hipchat.NotificationRequest{Message: msg}
	_, err := c.Room.Notification(roomName, msgReq)
	if err != nil {
		panic(err)
	}
}

func MonitorTrain(train *Train) {
	for {
		select {
	    case <- train.LeavingTimer.C:
	    	var buffer bytes.Buffer
	    	start := fmt.Sprintf("The train to %v has left the station with ", train.DisplayDestination)
	    	buffer.WriteString(start)
	    	buffer.WriteString(train.PassengerString())
	    	buffer.WriteString(" on it!")
	    	PostMessage(buffer.String())
	    	station.DeleteTrain(train.MapDestination)
	    	return
	    case <- train.ReminderTimer.C:
            PostMessage(fmt.Sprintf("Reminder, the next train to %v leaves in one minute", train.DisplayDestination))
	    default:
		}
	}
}

func GetDestinationAndTime(start int, messageParts []string, getTime bool) (string, int, error) {
	var dest bytes.Buffer
	for i := start; i < len(messageParts); i++ {
		if getTime {
			num, err := strconv.Atoi(messageParts[i])
			if err == nil && i == len(messageParts) - 1 {
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
	return "", 0, fmt.Errorf("Couldn't parse dest and/or time to departure")
}

func Handler(w rest.ResponseWriter, r *rest.Request) {
	var webMsg WebhookMessage
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&webMsg)	
	if err != nil {
		PostMessage(err.Error())
		return
	}

	author := webMsg.Item.MessageStruct.From.MentionName
	insufficientParams := fmt.Sprintf("%v messed up and forgot to provide the sufficient number of params", author)
	messageParts := strings.Split(webMsg.Item.MessageStruct.Message, " ")

	var msg string
	if len(messageParts) < 2 {
		PostMessage(insufficientParams)
		return 
	}
	cmd := strings.ToLower(messageParts[1])
	malformed := "Your command is malformed or not found, please view the help message (/train help) for more details"
	notFound := "That train doesn't exist, please try again"
	switch cmd {
	case "help":
		msg = "Usage: /train start <destination> <#minutes> || /train join <destination> || /train passengers <destination>"
		PostMessage(msg)
	case "passengers":
		dest, _, err := GetDestinationAndTime(2, messageParts, false)
		if err != nil {
			PostMessage(err.Error())
			break
		}
		train, ok := station.Trains[strings.ToLower(dest)]
		if !ok {
			PostMessage(notFound)
		} else {
			if len(train.Passengers) == 1 {
				msg = fmt.Sprintf("%v is on the train to %v", train.PassengerString(), train.DisplayDestination)
			} else {
				msg = fmt.Sprintf("%v are on the train to %v", train.PassengerString(), train.DisplayDestination)
			}
			PostMessage(msg)
		}
	case "join":
		dest, _, err := GetDestinationAndTime(2, messageParts, false)
		if err != nil {
			PostMessage(err.Error())
			break
		}
		train, ok := station.Trains[strings.ToLower(dest)]
		if ok {
			err := train.NewPassenger(author)
			if err == nil {
				msg = fmt.Sprintf("%s jumped on the train to %s", author, train.DisplayDestination)
				PostMessage(msg)
			} else {
				msg = err.Error()
				PostMessage(msg)	
			}
		} else {
			PostMessage(notFound)
		} 	
	case "start":
		dest, length, err := GetDestinationAndTime(2, messageParts, true)
		if err != nil {
			PostMessage(malformed)
			break
		}
		if length <= 0 {
			msg = fmt.Sprintf("Please specify a time greater than 0 mins")
			PostMessage(msg)
			break
		}
		_, ok := station.Trains[strings.ToLower(dest)]
		if ok {
			msg = fmt.Sprintf("There's already a train to %v!", dest)
			PostMessage(msg)
			break
		} else { 
			train := NewTrain(author, length, dest)
			err = station.AddTrain(train) 
			if err != nil {
				PostMessage(err.Error())
				break
			} else {
				msg = fmt.Sprintf("%s has started a train to %v that leaves in %v minutes!", author, train.DisplayDestination, length)
				PostMessage(msg)
				go MonitorTrain(train)
			}
		}
	default:
		PostMessage(malformed)
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
    bind := fmt.Sprintf("%s:%s",ip,port)
	err = http.ListenAndServe(bind, api.MakeHandler())
	if err != nil {
    	log.Println(err)
    }
}