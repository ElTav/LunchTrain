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
Manually depart a train
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
	Passengers: make(map[string]*Train),
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
	Delete chan struct{}
	MapDestination string
	DisplayDestination string
	PassengerSet map[string]struct{}
}

func NewTrain(conductor string, departure int, dest string) *Train {
	timer := time.NewTimer(time.Minute * time.Duration(departure))
	timer2 := time.NewTimer(time.Minute * time.Duration(departure - 1))	
	trainMap := make(map[string]struct{})
	trainMap[conductor] = struct{}{}
	return &Train{
		Lock: &sync.Mutex{},
		LeavingTimer: timer,
		ReminderTimer: timer2,
		Delete: make(chan struct{}),
		MapDestination: strings.ToLower(dest),
		DisplayDestination: dest,
		PassengerSet: trainMap,
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
	   if i != len(t.PassengerSet) - 1 {
	    	buffer.WriteString(", ")
	    }
	   if i == len(t.PassengerSet) - 2 {
	    	buffer.WriteString("and ")
	   }
	   i++
	}
	return buffer.String()
	 
}

type Station struct {
	Lock *sync.Mutex
	Passengers map[string]*Train
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
		log.Printf("The train to %s doesn't exist so it can't be removed", dest)
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
		case <- train.Delete:
			return
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

func DitchTrain(conductor string) {
	old := station.Passengers[conductor]
	var finalMsg bytes.Buffer
	msg := fmt.Sprintf("%v has decided to ditch their train to %v.", conductor, old.DisplayDestination)
	finalMsg.WriteString(msg)
	old.Lock.Lock()
	delete(old.PassengerSet, conductor)
	old.Lock.Unlock()
	if len(old.PassengerSet) == 0 {
		msg = fmt.Sprintf("It crashed and burned. There were no fatalities.")
		finalMsg.WriteString(msg)
		station.DeleteTrain(old.MapDestination)
		old.Delete <- struct{}{}
	}
	PostMessage(msg)
}

func Handler(w rest.ResponseWriter, r *rest.Request) {
	var webMsg WebhookMessage
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&webMsg)	
	if err != nil {
		log.Printf(err.Error())
		PostMessage(err.Error())
		return
	}

	conductor := webMsg.Item.MessageStruct.From.MentionName
	insufficientParams := fmt.Sprintf("%v messed up and forgot to provide the sufficient number of params", conductor)
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
		msg = "Usage: /train start <destination> <#minutes> || /train join <destination> || /train passengers <destination> || /train active"
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
			if len(train.PassengerSet) == 1 {
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
			_, ok := station.Passengers[conductor]
			if ok {
				DitchTrain(conductor)
			}
			err := train.NewPassenger(conductor)
			if err == nil {
				msg = fmt.Sprintf("%s jumped on the train to %s", conductor, train.DisplayDestination)
				station.Lock.Lock()
				station.Passengers[conductor] = train
				station.Lock.Unlock()
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
			_, ok := station.Passengers[conductor]
			if ok {
				DitchTrain(conductor)
			}
			train := NewTrain(conductor, length, dest)
			err = station.AddTrain(train)
			station.Lock.Lock()
			station.Passengers[conductor] = train
			station.Lock.Unlock()
			if err != nil {
				PostMessage(err.Error())
				break
			} else {
				msg = fmt.Sprintf("%s has started a train to %v that leaves in %v minutes!", conductor, train.DisplayDestination, length)
				PostMessage(msg)
				go MonitorTrain(train)
			}
		}
	case "active":
		if len(messageParts) != 2 {
			PostMessage(malformed)
			break
		}
		if len(station.Trains) == 0 {
			msg = fmt.Sprintf("There are currently no active trains")
			PostMessage(msg)
		} else {
			var finalMsg bytes.Buffer
			finalMsg.WriteString("There are trains to: ")
			i := 0
			for _, v := range station.Trains {
				if len(station.Trains) == 1 {
					msg = fmt.Sprintf("There is currently a train to %v (with %v on it)", v.DisplayDestination, v.PassengerString())
					PostMessage(msg)
					return
				} else {
					finalMsg.WriteString(fmt.Sprintf("%v (with %v on it)", v.DisplayDestination, v.PassengerString()))
				}
				if i == len(station.Trains) - 2 {
					finalMsg.WriteString("and ")
	 		    }
				if i != len(station.Trains) - 1 {
					finalMsg.WriteString(", ")
				}
				i++
				
			}
			PostMessage(finalMsg.String())
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