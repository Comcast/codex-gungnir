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
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Comcast/webpa-common/logging"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/stretchr/testify/assert"

	"github.com/Comcast/codex/db"
)

func TestGetStatusInfo(t *testing.T) {
	getRecordsErr := errors.New("get records of type test error")

	testassert := assert.New(t)
	futureTime := time.Now().Add(time.Duration(50000) * time.Minute)
	previousTime, err := time.Parse(time.RFC3339Nano, "2019-02-13T21:19:02.614191735Z")
	testassert.Nil(err)

	goodData, err := json.Marshal(&goodEvent)
	testassert.Nil(err)
	event := goodEvent
	event.Payload = []byte("")
	emptyPayloadData, err := json.Marshal(&event)
	testassert.Nil(err)
	badData, err := json.Marshal("")
	testassert.Nil(err)

	tests := []struct {
		description          string
		recordsToReturn      []db.Record
		getRecordsErr        error
		expectedStatus       Status
		expectedErr          error
		expectedServerStatus int
	}{
		{
			description:          "Get Records Error",
			getRecordsErr:        getRecordsErr,
			expectedStatus:       Status{},
			expectedErr:          getRecordsErr,
			expectedServerStatus: http.StatusInternalServerError,
		},
		{
			description:          "Empty Records Error",
			expectedStatus:       Status{},
			expectedErr:          errors.New("No events found"),
			expectedServerStatus: http.StatusNotFound,
		},
		{
			description: "Expired Records Error",
			recordsToReturn: []db.Record{
				db.Record{
					DeathDate: previousTime,
				},
			},
			expectedStatus:       Status{},
			expectedErr:          errors.New("No events found"),
			expectedServerStatus: http.StatusNotFound,
		},
		{
			description: "Unmarshal Event Error",
			recordsToReturn: []db.Record{
				{
					DeathDate: futureTime,
					Data:      badData,
				},
			},
			expectedStatus:       Status{},
			expectedErr:          errors.New("No events found"),
			expectedServerStatus: http.StatusNotFound,
		},
		{
			description: "Unmarshal Payload Error",
			recordsToReturn: []db.Record{
				{
					ID:        1234,
					Type:      db.EventState,
					DeathDate: futureTime,
					Data:      emptyPayloadData,
				},
			},
			expectedStatus:       Status{},
			expectedErr:          errors.New("No events found"),
			expectedServerStatus: http.StatusNotFound,
		},
		{
			description: "Success",
			recordsToReturn: []db.Record{
				{
					ID:        1234,
					Type:      db.EventState,
					DeathDate: futureTime,
					Data:      goodData,
				},
				{
					ID:        1234,
					Type:      db.EventState,
					DeathDate: futureTime,
					Data:      goodData,
				},
			},
			expectedStatus: Status{
				DeviceID:          "test",
				State:             "online",
				Since:             time.Time{},
				Now:               time.Now(),
				LastOfflineReason: "ping miss",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			mockGetter := new(mockRecordGetter)
			mockGetter.On("GetRecordsOfType", "test", db.EventState).Return(tc.recordsToReturn, tc.getRecordsErr).Once()
			app := App{
				eventGetter: mockGetter,
				logger:      logging.DefaultLogger(),
			}
			status, err := app.getStatusInfo("test")

			// can't assert over the full status, since we can't check Now
			assert.Equal(tc.expectedStatus.DeviceID, status.DeviceID)
			assert.Equal(tc.expectedStatus.State, status.State)
			assert.Equal(tc.expectedStatus.Since, status.Since)
			assert.Equal(tc.expectedStatus.LastOfflineReason, status.LastOfflineReason)

			if tc.expectedErr == nil || err == nil {
				assert.Equal(tc.expectedErr, err)
			} else {
				assert.Contains(err.Error(), tc.expectedErr.Error())
			}
			if tc.expectedServerStatus > 0 {
				statusCodeErr, ok := err.(kithttp.StatusCoder)
				assert.True(ok, "expected error to have a status code")
				assert.Equal(tc.expectedServerStatus, statusCodeErr.StatusCode())
			}
		})
	}
}

func TestHandleGetStatus(t *testing.T) {
	testassert := assert.New(t)
	futureTime := time.Now().Add(time.Duration(50000) * time.Minute)
	goodData, err := json.Marshal(&goodEvent)
	testassert.Nil(err)

	tests := []struct {
		description        string
		recordsToReturn    []db.Record
		expectedStatusCode int
		expectedBody       []byte
	}{
		{
			description:        "Get Device Info Error",
			expectedStatusCode: http.StatusNotFound,
		},
		{
			description: "Success",
			recordsToReturn: []db.Record{
				{
					ID:        1234,
					DeathDate: futureTime,
					Data:      goodData,
				},
			},
			expectedStatusCode: http.StatusOK,
			expectedBody:       goodData,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			mockGetter := new(mockRecordGetter)
			mockGetter.On("GetRecordsOfType", "", 1).Return(tc.recordsToReturn, nil).Once()
			app := App{
				eventGetter: mockGetter,
				logger:      logging.DefaultLogger(),
			}
			rr := httptest.NewRecorder()
			request, err := http.NewRequest(http.MethodGet, "/1234/status", nil)
			assert.Nil(err)
			app.handleGetStatus(rr, request)
		})
	}
}