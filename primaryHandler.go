/**
 * Copyright 2019 Comcast Cable Communications Management, LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Comcast/codex/cipher"

	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/wrp-go/wrp"
	"github.com/goph/emperror"

	"github.com/Comcast/codex/db"
	"github.com/go-kit/kit/log"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/gorilla/mux"
)

//go:generate swagger generate spec -m -o swagger.spec

type App struct {
	eventGetter db.RecordGetter
	logger      log.Logger
	getLimit    int
	decrypters  cipher.Ciphers

	measures *Measures
}

// Event is the extension of wrp message
//     https://github.com/Comcast/wrp-c/wiki/Web-Routing-Protocol
//
// swagger:response Event
type Event struct {
	wrp.Message

	// BirthDate the time codex received the message
	//
	// required: false
	// example: 1555639704
	BirthDate int64 `wrp:"birth_date,omitempty" json:"birth_date,omitempty"`
}

// swagger:parameters getEvents getStatus
type DeviceIdParam struct {
	// device id passed by caller
	//
	// in: path
	// required: true
	DeviceID string `json:"deviceID"`
}

// EventResponse is what is returned on a successful response
//
// swagger:response EventResponse
type EventResponse struct {
	// in:body
	Body []Event
}

// ErrResponse is the information passed to the client on an error
//
// swagger:response ErrResponse
type ErrResponse struct {
	// The http code of the response
	//
	// required: true
	Code int `json:"code"`
}

func (app *App) getDeviceInfo(deviceID string) ([]Event, error) {

	records, hErr := app.eventGetter.GetRecords(deviceID, app.getLimit)
	events := []Event{}

	// if both have errors or are empty, return an error
	if hErr != nil {
		return events, serverErr{emperror.WrapWith(hErr, "Failed to get events", "device id", deviceID),
			http.StatusInternalServerError}
	}

	// if all is good, unmarshal everything
	for _, record := range records {
		// if the record is expired, don't include it
		if time.Unix(record.DeathDate, 0).Before(time.Now()) {
			continue
		}

		event := Event{
			BirthDate: record.BirthDate,
		}
		decrypter, ok := app.decrypters.Get(cipher.ParseAlogrithmType(record.Alg), record.KID)
		if !ok {
			app.measures.GetDecryptFailure.Add(1.0)
			logging.Error(app.logger).Log(logging.MessageKey(), "Failed to get decrypter", logging.ErrorKey())
			event.Type = wrp.UnknownMessageType
			events = append(events, event)
			continue
		}
		data, err := decrypter.DecryptMessage(record.Data, record.Nonce)
		if err != nil {
			app.measures.DecryptFailure.Add(1.0)
			logging.Error(app.logger).Log(logging.MessageKey(), "Failed to decrypt event", logging.ErrorKey(), err.Error())
			event.Type = wrp.UnknownMessageType
			events = append(events, event)
			continue
		}

		decoder := wrp.NewDecoderBytes(data, wrp.Msgpack)
		err = decoder.Decode(&event)
		if err != nil {
			app.measures.UnmarshalFailure.Add(1.0)
			logging.Error(app.logger, emperror.Context(err)...).Log(logging.MessageKey(), "Failed to decode decrypted event", logging.ErrorKey(), err.Error())
			event.Type = wrp.UnknownMessageType
			events = append(events, event)
			continue
		}

		events = append(events, event)
	}

	if len(events) == 0 {
		return events, serverErr{emperror.With(errors.New("No events found for device id"), "device id", deviceID),
			http.StatusNotFound}
	}
	return events, nil
}

/*
 * swagger:route GET /device/{deviceID}/events device getEvents
 *
 * Get all of the events related to a specific device id.
 *
 * Parameters: deviceID
 *
 * Produces:
 *    - application/json
 *
 * Schemes: https
 *
 * Security:
 *    bearer_token:
 *
 * Responses:
 *    200: EventResponse
 *    404: ErrResponse
 *    500: ErrResponse
 *
 */
func (app *App) handleGetEvents(writer http.ResponseWriter, request *http.Request) {
	var (
		d   []Event
		err error
	)
	vars := mux.Vars(request)
	id := strings.ToLower(vars["deviceID"])
	if id == "" {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	if d, err = app.getDeviceInfo(id); err != nil {
		logging.Error(app.logger, emperror.Context(err)...).Log(logging.MessageKey(),
			"Failed to get status info", logging.ErrorKey(), err.Error())
		writer.Header().Add("X-Codex-Error", err.Error())
		if val, ok := err.(kithttp.StatusCoder); ok {
			writer.WriteHeader(val.StatusCode())
			return
		}
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}

	data, err := json.Marshal(&d)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	writer.Write(data)
}
