// Copyright 2021 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// JetStreamManager is the public interface for managing JetStream streams & consumers.
type JetStreamManager interface {
	// AddStream creates a stream.
	AddStream(cfg *StreamConfig, opts ...JSOpt) (*StreamInfo, error)

	// UpdateStream updates a stream.
	UpdateStream(cfg *StreamConfig, opts ...JSOpt) (*StreamInfo, error)

	// DeleteStream deletes a stream.
	DeleteStream(name string, opts ...JSOpt) error

	// StreamInfo retrieves information from a stream.
	StreamInfo(stream string, opts ...JSOpt) (*StreamInfo, error)

	// PurgeStream purges a stream messages.
	PurgeStream(name string, opts ...JSOpt) error

	// StreamsInfo can be used to retrieve a list of StreamInfo objects.
	StreamsInfo(opts ...JSOpt) <-chan *StreamInfo

	// StreamNames is used to retrieve a list of Stream names.
	StreamNames(opts ...JSOpt) <-chan string

	// GetMsg retrieves a raw stream message stored in JetStream by sequence number.
	GetMsg(name string, seq uint64, opts ...JSOpt) (*RawStreamMsg, error)

	// DeleteMsg erases a message from a stream.
	DeleteMsg(name string, seq uint64, opts ...JSOpt) error

	// AddConsumer adds a consumer to a stream.
	AddConsumer(stream string, cfg *ConsumerConfig, opts ...JSOpt) (*ConsumerInfo, error)

	// DeleteConsumer deletes a consumer.
	DeleteConsumer(stream, consumer string, opts ...JSOpt) error

	// ConsumerInfo retrieves information of a consumer from a stream.
	ConsumerInfo(stream, name string, opts ...JSOpt) (*ConsumerInfo, error)

	// ConsumersInfo is used to retrieve a list of ConsumerInfo objects.
	ConsumersInfo(stream string, opts ...JSOpt) <-chan *ConsumerInfo

	// ConsumerNames is used to retrieve a list of Consumer names.
	ConsumerNames(stream string, opts ...JSOpt) <-chan string

	// AccountInfo retrieves info about the JetStream usage from an account.
	AccountInfo(opts ...JSOpt) (*AccountInfo, error)
}

// StreamConfig will determine the properties for a stream.
// There are sensible defaults for most. If no subjects are
// given the name will be used as the only subject.
type StreamConfig struct {
	Name         string          `json:"name"`
	Subjects     []string        `json:"subjects,omitempty"`
	Retention    RetentionPolicy `json:"retention"`
	MaxConsumers int             `json:"max_consumers"`
	MaxMsgs      int64           `json:"max_msgs"`
	MaxBytes     int64           `json:"max_bytes"`
	Discard      DiscardPolicy   `json:"discard"`
	MaxAge       time.Duration   `json:"max_age"`
	MaxMsgSize   int32           `json:"max_msg_size,omitempty"`
	Storage      StorageType     `json:"storage"`
	Replicas     int             `json:"num_replicas"`
	NoAck        bool            `json:"no_ack,omitempty"`
	Template     string          `json:"template_owner,omitempty"`
	Duplicates   time.Duration   `json:"duplicate_window,omitempty"`
	Placement    *Placement      `json:"placement,omitempty"`
	Mirror       *StreamSource   `json:"mirror,omitempty"`
	Sources      []*StreamSource `json:"sources,omitempty"`
}

// Placement is used to guide placement of streams in clustered JetStream.
type Placement struct {
	Cluster string   `json:"cluster"`
	Tags    []string `json:"tags,omitempty"`
}

// StreamSource dictates how streams can source from other streams.
type StreamSource struct {
	Name          string          `json:"name"`
	OptStartSeq   uint64          `json:"opt_start_seq,omitempty"`
	OptStartTime  *time.Time      `json:"opt_start_time,omitempty"`
	FilterSubject string          `json:"filter_subject,omitempty"`
	External      *ExternalStream `json:"external,omitempty"`
}

// ExternalStream allows you to qualify access to a stream source in another
// account.
type ExternalStream struct {
	APIPrefix     string `json:"api"`
	DeliverPrefix string `json:"deliver"`
}

// apiError is included in all API responses if there was an error.
type apiError struct {
	Code        int    `json:"code"`
	Description string `json:"description,omitempty"`
}

// apiResponse is a standard response from the JetStream JSON API
type apiResponse struct {
	Type  string    `json:"type"`
	Error *apiError `json:"error,omitempty"`
}

// apiPaged includes variables used to create paged responses from the JSON API
type apiPaged struct {
	Total  int `json:"total"`
	Offset int `json:"offset"`
	Limit  int `json:"limit"`
}

// apiPagedRequest includes parameters allowing specific pages to be requested
// from APIs responding with apiPaged.
type apiPagedRequest struct {
	Offset int `json:"offset"`
}

// AccountInfo contains info about the JetStream usage from the current account.
type AccountInfo struct {
	Memory    uint64        `json:"memory"`
	Store     uint64        `json:"storage"`
	Streams   int           `json:"streams"`
	Consumers int           `json:"consumers"`
	API       APIStats      `json:"api"`
	Limits    AccountLimits `json:"limits"`
}

// APIStats reports on API calls to JetStream for this account.
type APIStats struct {
	Total  uint64 `json:"total"`
	Errors uint64 `json:"errors"`
}

// AccountLimits includes the JetStream limits of the current account.
type AccountLimits struct {
	MaxMemory    int64 `json:"max_memory"`
	MaxStore     int64 `json:"max_storage"`
	MaxStreams   int   `json:"max_streams"`
	MaxConsumers int   `json:"max_consumers"`
}

type accountInfoResponse struct {
	apiResponse
	AccountInfo
}

// AccountInfo retrieves info about the JetStream usage from the current account.
func (js *js) AccountInfo(opts ...JSOpt) (*AccountInfo, error) {
	o, cancel, err := getJSContextOpts(js.opts, opts...)
	if err != nil {
		return nil, err
	}
	if cancel != nil {
		defer cancel()
	}

	resp, err := js.nc.RequestWithContext(o.ctx, js.apiSubj(apiAccountInfo), nil)
	if err != nil {
		return nil, err
	}
	var info accountInfoResponse
	if err := json.Unmarshal(resp.Data, &info); err != nil {
		return nil, err
	}
	if info.Error != nil {
		var err error
		if strings.Contains(info.Error.Description, "not enabled for") {
			err = ErrJetStreamNotEnabled
		} else {
			err = errors.New(info.Error.Description)
		}
		return nil, err
	}

	return &info.AccountInfo, nil
}

type createConsumerRequest struct {
	Stream string          `json:"stream_name"`
	Config *ConsumerConfig `json:"config"`
}

type consumerResponse struct {
	apiResponse
	*ConsumerInfo
}

// AddConsumer will add a JetStream consumer.
func (js *js) AddConsumer(stream string, cfg *ConsumerConfig, opts ...JSOpt) (*ConsumerInfo, error) {
	o, cancel, err := getJSContextOpts(js.opts, opts...)
	if err != nil {
		return nil, err
	}
	if cancel != nil {
		defer cancel()
	}

	if stream == _EMPTY_ {
		return nil, ErrStreamNameRequired
	}
	req, err := json.Marshal(&createConsumerRequest{Stream: stream, Config: cfg})
	if err != nil {
		return nil, err
	}

	var ccSubj string
	if cfg != nil && cfg.Durable != _EMPTY_ {
		if strings.Contains(cfg.Durable, ".") {
			return nil, ErrInvalidDurableName
		}
		ccSubj = fmt.Sprintf(apiDurableCreateT, stream, cfg.Durable)
	} else {
		ccSubj = fmt.Sprintf(apiConsumerCreateT, stream)
	}

	resp, err := js.nc.RequestWithContext(o.ctx, js.apiSubj(ccSubj), req)
	if err != nil {
		if err == ErrNoResponders {
			err = ErrJetStreamNotEnabled
		}
		return nil, err
	}
	var info consumerResponse
	err = json.Unmarshal(resp.Data, &info)
	if err != nil {
		return nil, err
	}
	if info.Error != nil {
		return nil, errors.New(info.Error.Description)
	}
	return info.ConsumerInfo, nil
}

// consumerDeleteResponse is the response for a Consumer delete request.
type consumerDeleteResponse struct {
	apiResponse
	Success bool `json:"success,omitempty"`
}

// DeleteConsumer deletes a Consumer.
func (js *js) DeleteConsumer(stream, consumer string, opts ...JSOpt) error {
	o, cancel, err := getJSContextOpts(js.opts, opts...)
	if err != nil {
		return err
	}
	if cancel != nil {
		defer cancel()
	}

	if stream == _EMPTY_ {
		return ErrStreamNameRequired
	}

	dcSubj := js.apiSubj(fmt.Sprintf(apiConsumerDeleteT, stream, consumer))
	r, err := js.nc.RequestWithContext(o.ctx, dcSubj, nil)
	if err != nil {
		return err
	}
	var resp consumerDeleteResponse
	if err := json.Unmarshal(r.Data, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return errors.New(resp.Error.Description)
	}
	return nil
}

// ConsumerInfo returns information about a Consumer.
func (js *js) ConsumerInfo(stream, consumer string, opts ...JSOpt) (*ConsumerInfo, error) {
	o, cancel, err := getJSContextOpts(js.opts, opts...)
	if err != nil {
		return nil, err
	}
	if cancel != nil {
		defer cancel()
	}
	return js.getConsumerInfoContext(o.ctx, stream, consumer)
}

// consumerLister fetches pages of ConsumerInfo objects. This object is not
// safe to use for multiple threads.
type consumerLister struct {
	stream string
	js     *js

	err      error
	offset   int
	page     []*ConsumerInfo
	pageInfo *apiPaged
}

// consumersRequest is the type used for Consumers requests.
type consumersRequest struct {
	apiPagedRequest
}

// consumerListResponse is the response for a Consumers List request.
type consumerListResponse struct {
	apiResponse
	apiPaged
	Consumers []*ConsumerInfo `json:"consumers"`
}

// Next fetches the next ConsumerInfo page.
func (c *consumerLister) Next() bool {
	if c.err != nil {
		return false
	}
	if c.stream == _EMPTY_ {
		c.err = ErrStreamNameRequired
		return false
	}
	if c.pageInfo != nil && c.offset >= c.pageInfo.Total {
		return false
	}

	req, err := json.Marshal(consumersRequest{
		apiPagedRequest: apiPagedRequest{Offset: c.offset},
	})
	if err != nil {
		c.err = err
		return false
	}

	var cancel context.CancelFunc
	ctx := c.js.opts.ctx
	if ctx == nil {
		ctx, cancel = context.WithTimeout(context.Background(), c.js.opts.wait)
		defer cancel()
	}

	clSubj := c.js.apiSubj(fmt.Sprintf(apiConsumerListT, c.stream))
	r, err := c.js.nc.RequestWithContext(ctx, clSubj, req)
	if err != nil {
		c.err = err
		return false
	}
	var resp consumerListResponse
	if err := json.Unmarshal(r.Data, &resp); err != nil {
		c.err = err
		return false
	}
	if resp.Error != nil {
		c.err = errors.New(resp.Error.Description)
		return false
	}

	c.pageInfo = &resp.apiPaged
	c.page = resp.Consumers
	c.offset += len(c.page)
	return true
}

// Page returns the current ConsumerInfo page.
func (c *consumerLister) Page() []*ConsumerInfo {
	return c.page
}

// Err returns any errors found while fetching pages.
func (c *consumerLister) Err() error {
	return c.err
}

// ConsumersInfo is used to retrieve a list of ConsumerInfo objects.
func (jsc *js) ConsumersInfo(stream string, opts ...JSOpt) <-chan *ConsumerInfo {
	o, cancel, err := getJSContextOpts(jsc.opts, opts...)
	if err != nil {
		return nil
	}

	ch := make(chan *ConsumerInfo)
	l := &consumerLister{js: &js{nc: jsc.nc, opts: o}, stream: stream}
	go func() {
		if cancel != nil {
			defer cancel()
		}
		defer close(ch)
		for l.Next() {
			for _, info := range l.Page() {
				select {
				case ch <- info:
				case <-o.ctx.Done():
					return
				}
			}
		}
	}()

	return ch
}

type consumerNamesLister struct {
	stream string
	js     *js

	err      error
	offset   int
	page     []string
	pageInfo *apiPaged
}

// consumerNamesListResponse is the response for a Consumers Names List request.
type consumerNamesListResponse struct {
	apiResponse
	apiPaged
	Consumers []string `json:"consumers"`
}

// Next fetches the next ConsumerInfo page.
func (c *consumerNamesLister) Next() bool {
	if c.err != nil {
		return false
	}
	if c.stream == _EMPTY_ {
		c.err = ErrStreamNameRequired
		return false
	}
	if c.pageInfo != nil && c.offset >= c.pageInfo.Total {
		return false
	}

	var cancel context.CancelFunc
	ctx := c.js.opts.ctx
	if ctx == nil {
		ctx, cancel = context.WithTimeout(context.Background(), c.js.opts.wait)
		defer cancel()
	}

	clSubj := c.js.apiSubj(fmt.Sprintf(apiConsumerNamesT, c.stream))
	r, err := c.js.nc.RequestWithContext(ctx, clSubj, nil)
	if err != nil {
		c.err = err
		return false
	}
	var resp consumerNamesListResponse
	if err := json.Unmarshal(r.Data, &resp); err != nil {
		c.err = err
		return false
	}
	if resp.Error != nil {
		c.err = errors.New(resp.Error.Description)
		return false
	}

	c.pageInfo = &resp.apiPaged
	c.page = resp.Consumers
	c.offset += len(c.page)
	return true
}

// Page returns the current ConsumerInfo page.
func (c *consumerNamesLister) Page() []string {
	return c.page
}

// Err returns any errors found while fetching pages.
func (c *consumerNamesLister) Err() error {
	return c.err
}

// ConsumerNames is used to retrieve a list of Consumer names.
func (jsc *js) ConsumerNames(stream string, opts ...JSOpt) <-chan string {
	o, cancel, err := getJSContextOpts(jsc.opts, opts...)
	if err != nil {
		return nil
	}

	ch := make(chan string)
	l := &consumerNamesLister{stream: stream, js: &js{nc: jsc.nc, opts: o}}
	go func() {
		if cancel != nil {
			defer cancel()
		}
		defer close(ch)
		for l.Next() {
			for _, info := range l.Page() {
				select {
				case ch <- info:
				case <-o.ctx.Done():
					return
				}
			}
		}
	}()

	return ch
}

// streamCreateResponse stream creation.
type streamCreateResponse struct {
	apiResponse
	*StreamInfo
}

func (js *js) AddStream(cfg *StreamConfig, opts ...JSOpt) (*StreamInfo, error) {
	o, cancel, err := getJSContextOpts(js.opts, opts...)
	if err != nil {
		return nil, err
	}
	if cancel != nil {
		defer cancel()
	}

	if cfg == nil || cfg.Name == _EMPTY_ {
		return nil, ErrStreamNameRequired
	}

	req, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	csSubj := js.apiSubj(fmt.Sprintf(apiStreamCreateT, cfg.Name))
	r, err := js.nc.RequestWithContext(o.ctx, csSubj, req)
	if err != nil {
		return nil, err
	}
	var resp streamCreateResponse
	if err := json.Unmarshal(r.Data, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, errors.New(resp.Error.Description)
	}
	return resp.StreamInfo, nil
}

type streamInfoResponse = streamCreateResponse

func (js *js) StreamInfo(stream string, opts ...JSOpt) (*StreamInfo, error) {
	o, cancel, err := getJSContextOpts(js.opts, opts...)
	if err != nil {
		return nil, err
	}
	if cancel != nil {
		defer cancel()
	}

	csSubj := js.apiSubj(fmt.Sprintf(apiStreamInfoT, stream))
	r, err := js.nc.RequestWithContext(o.ctx, csSubj, nil)
	if err != nil {
		return nil, err
	}
	var resp streamInfoResponse
	if err := json.Unmarshal(r.Data, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, errors.New(resp.Error.Description)
	}
	return resp.StreamInfo, nil
}

// StreamInfo shows config and current state for this stream.
type StreamInfo struct {
	Config  StreamConfig        `json:"config"`
	Created time.Time           `json:"created"`
	State   StreamState         `json:"state"`
	Cluster *ClusterInfo        `json:"cluster,omitempty"`
	Mirror  *StreamSourceInfo   `json:"mirror,omitempty"`
	Sources []*StreamSourceInfo `json:"sources,omitempty"`
}

// StreamSourceInfo shows information about an upstream stream source.
type StreamSourceInfo struct {
	Name   string        `json:"name"`
	Lag    uint64        `json:"lag"`
	Active time.Duration `json:"active"`
}

// StreamState is information about the given stream.
type StreamState struct {
	Msgs      uint64    `json:"messages"`
	Bytes     uint64    `json:"bytes"`
	FirstSeq  uint64    `json:"first_seq"`
	FirstTime time.Time `json:"first_ts"`
	LastSeq   uint64    `json:"last_seq"`
	LastTime  time.Time `json:"last_ts"`
	Consumers int       `json:"consumer_count"`
}

// ClusterInfo shows information about the underlying set of servers
// that make up the stream or consumer.
type ClusterInfo struct {
	Name     string      `json:"name,omitempty"`
	Leader   string      `json:"leader,omitempty"`
	Replicas []*PeerInfo `json:"replicas,omitempty"`
}

// PeerInfo shows information about all the peers in the cluster that
// are supporting the stream or consumer.
type PeerInfo struct {
	Name    string        `json:"name"`
	Current bool          `json:"current"`
	Offline bool          `json:"offline,omitempty"`
	Active  time.Duration `json:"active"`
	Lag     uint64        `json:"lag,omitempty"`
}

// UpdateStream updates a Stream.
func (js *js) UpdateStream(cfg *StreamConfig, opts ...JSOpt) (*StreamInfo, error) {
	o, cancel, err := getJSContextOpts(js.opts, opts...)
	if err != nil {
		return nil, err
	}
	if cancel != nil {
		defer cancel()
	}

	if cfg == nil || cfg.Name == _EMPTY_ {
		return nil, ErrStreamNameRequired
	}

	req, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	usSubj := js.apiSubj(fmt.Sprintf(apiStreamUpdateT, cfg.Name))
	r, err := js.nc.RequestWithContext(o.ctx, usSubj, req)
	if err != nil {
		return nil, err
	}
	var resp streamInfoResponse
	if err := json.Unmarshal(r.Data, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, errors.New(resp.Error.Description)
	}
	return resp.StreamInfo, nil
}

// streamDeleteResponse is the response for a Stream delete request.
type streamDeleteResponse struct {
	apiResponse
	Success bool `json:"success,omitempty"`
}

// DeleteStream deletes a Stream.
func (js *js) DeleteStream(name string, opts ...JSOpt) error {
	o, cancel, err := getJSContextOpts(js.opts, opts...)
	if err != nil {
		return err
	}
	if cancel != nil {
		defer cancel()
	}

	if name == _EMPTY_ {
		return ErrStreamNameRequired
	}

	dsSubj := js.apiSubj(fmt.Sprintf(apiStreamDeleteT, name))
	r, err := js.nc.RequestWithContext(o.ctx, dsSubj, nil)
	if err != nil {
		return err
	}
	var resp streamDeleteResponse
	if err := json.Unmarshal(r.Data, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return errors.New(resp.Error.Description)
	}
	return nil
}

type apiMsgGetRequest struct {
	Seq uint64 `json:"seq"`
}

// RawStreamMsg is a raw message stored in JetStream.
type RawStreamMsg struct {
	Subject  string
	Sequence uint64
	Header   http.Header
	Data     []byte
	Time     time.Time
}

// storedMsg is a raw message stored in JetStream.
type storedMsg struct {
	Subject  string    `json:"subject"`
	Sequence uint64    `json:"seq"`
	Header   []byte    `json:"hdrs,omitempty"`
	Data     []byte    `json:"data,omitempty"`
	Time     time.Time `json:"time"`
}

// apiMsgGetResponse is the response for a Stream get request.
type apiMsgGetResponse struct {
	apiResponse
	Message *storedMsg `json:"message,omitempty"`
	Success bool       `json:"success,omitempty"`
}

// GetMsg retrieves a raw stream message stored in JetStream by sequence number.
func (js *js) GetMsg(name string, seq uint64, opts ...JSOpt) (*RawStreamMsg, error) {
	o, cancel, err := getJSContextOpts(js.opts, opts...)
	if err != nil {
		return nil, err
	}
	if cancel != nil {
		defer cancel()
	}

	if name == _EMPTY_ {
		return nil, ErrStreamNameRequired
	}

	req, err := json.Marshal(&apiMsgGetRequest{Seq: seq})
	if err != nil {
		return nil, err
	}

	dsSubj := js.apiSubj(fmt.Sprintf(apiMsgGetT, name))
	r, err := js.nc.RequestWithContext(o.ctx, dsSubj, req)
	if err != nil {
		return nil, err
	}

	var resp apiMsgGetResponse
	if err := json.Unmarshal(r.Data, &resp); err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, errors.New(resp.Error.Description)
	}

	msg := resp.Message

	var hdr http.Header
	if msg.Header != nil {
		hdr, err = decodeHeadersMsg(msg.Header)
		if err != nil {
			return nil, err
		}
	}

	return &RawStreamMsg{
		Subject:  msg.Subject,
		Sequence: msg.Sequence,
		Header:   hdr,
		Data:     msg.Data,
		Time:     msg.Time,
	}, nil
}

type msgDeleteRequest struct {
	Seq uint64 `json:"seq"`
}

// msgDeleteResponse is the response for a Stream delete request.
type msgDeleteResponse struct {
	apiResponse
	Success bool `json:"success,omitempty"`
}

// DeleteMsg deletes a message from a stream.
func (js *js) DeleteMsg(name string, seq uint64, opts ...JSOpt) error {
	o, cancel, err := getJSContextOpts(js.opts, opts...)
	if err != nil {
		return err
	}
	if cancel != nil {
		defer cancel()
	}

	if name == _EMPTY_ {
		return ErrStreamNameRequired
	}

	req, err := json.Marshal(&msgDeleteRequest{Seq: seq})
	if err != nil {
		return err
	}

	dsSubj := js.apiSubj(fmt.Sprintf(apiMsgDeleteT, name))
	r, err := js.nc.RequestWithContext(o.ctx, dsSubj, req)
	if err != nil {
		return err
	}
	var resp msgDeleteResponse
	if err := json.Unmarshal(r.Data, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return errors.New(resp.Error.Description)
	}
	return nil
}

type streamPurgeResponse struct {
	apiResponse
	Success bool   `json:"success,omitempty"`
	Purged  uint64 `json:"purged"`
}

// PurgeStream purges messages on a Stream.
func (js *js) PurgeStream(name string, opts ...JSOpt) error {
	o, cancel, err := getJSContextOpts(js.opts, opts...)
	if err != nil {
		return err
	}
	if cancel != nil {
		defer cancel()
	}

	psSubj := js.apiSubj(fmt.Sprintf(apiStreamPurgeT, name))
	r, err := js.nc.RequestWithContext(o.ctx, psSubj, nil)
	if err != nil {
		return err
	}
	var resp streamPurgeResponse
	if err := json.Unmarshal(r.Data, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return errors.New(resp.Error.Description)
	}
	return nil
}

// streamLister fetches pages of StreamInfo objects. This object is not safe
// to use for multiple threads.
type streamLister struct {
	js   *js
	page []*StreamInfo
	err  error

	offset   int
	pageInfo *apiPaged
}

// streamListResponse list of detailed stream information.
// A nil request is valid and means all streams.
type streamListResponse struct {
	apiResponse
	apiPaged
	Streams []*StreamInfo `json:"streams"`
}

// streamNamesRequest is used for Stream Name requests.
type streamNamesRequest struct {
	apiPagedRequest
	// These are filters that can be applied to the list.
	Subject string `json:"subject,omitempty"`
}

// Next fetches the next StreamInfo page.
func (s *streamLister) Next() bool {
	if s.err != nil {
		return false
	}
	if s.pageInfo != nil && s.offset >= s.pageInfo.Total {
		return false
	}

	req, err := json.Marshal(streamNamesRequest{
		apiPagedRequest: apiPagedRequest{Offset: s.offset},
	})
	if err != nil {
		s.err = err
		return false
	}

	var cancel context.CancelFunc
	ctx := s.js.opts.ctx
	if ctx == nil {
		ctx, cancel = context.WithTimeout(context.Background(), s.js.opts.wait)
		defer cancel()
	}

	slSubj := s.js.apiSubj(apiStreamList)
	r, err := s.js.nc.RequestWithContext(ctx, slSubj, req)
	if err != nil {
		s.err = err
		return false
	}
	var resp streamListResponse
	if err := json.Unmarshal(r.Data, &resp); err != nil {
		s.err = err
		return false
	}
	if resp.Error != nil {
		s.err = errors.New(resp.Error.Description)
		return false
	}

	s.pageInfo = &resp.apiPaged
	s.page = resp.Streams
	s.offset += len(s.page)
	return true
}

// Page returns the current StreamInfo page.
func (s *streamLister) Page() []*StreamInfo {
	return s.page
}

// Err returns any errors found while fetching pages.
func (s *streamLister) Err() error {
	return s.err
}

// StreamsInfo can be used to retrieve a list of StreamInfo objects.
func (jsc *js) StreamsInfo(opts ...JSOpt) <-chan *StreamInfo {
	o, cancel, err := getJSContextOpts(jsc.opts, opts...)
	if err != nil {
		return nil
	}

	ch := make(chan *StreamInfo)
	l := &streamLister{js: &js{nc: jsc.nc, opts: o}}
	go func() {
		if cancel != nil {
			defer cancel()
		}
		defer close(ch)
		for l.Next() {
			for _, info := range l.Page() {
				select {
				case ch <- info:
				case <-o.ctx.Done():
					return
				}
			}
		}
	}()

	return ch
}

type streamNamesLister struct {
	js *js

	err      error
	offset   int
	page     []string
	pageInfo *apiPaged
}

// Next fetches the next ConsumerInfo page.
func (l *streamNamesLister) Next() bool {
	if l.err != nil {
		return false
	}
	if l.pageInfo != nil && l.offset >= l.pageInfo.Total {
		return false
	}

	var cancel context.CancelFunc
	ctx := l.js.opts.ctx
	if ctx == nil {
		ctx, cancel = context.WithTimeout(context.Background(), l.js.opts.wait)
		defer cancel()
	}

	r, err := l.js.nc.RequestWithContext(ctx, l.js.apiSubj(apiStreams), nil)
	if err != nil {
		l.err = err
		return false
	}
	var resp streamNamesResponse
	if err := json.Unmarshal(r.Data, &resp); err != nil {
		l.err = err
		return false
	}
	if resp.Error != nil {
		l.err = errors.New(resp.Error.Description)
		return false
	}

	l.pageInfo = &resp.apiPaged
	l.page = resp.Streams
	l.offset += len(l.page)
	return true
}

// Page returns the current ConsumerInfo page.
func (l *streamNamesLister) Page() []string {
	return l.page
}

// Err returns any errors found while fetching pages.
func (l *streamNamesLister) Err() error {
	return l.err
}

// StreamNames is used to retrieve a list of Stream names.
func (jsc *js) StreamNames(opts ...JSOpt) <-chan string {
	o, cancel, err := getJSContextOpts(jsc.opts, opts...)
	if err != nil {
		return nil
	}

	ch := make(chan string)
	l := &streamNamesLister{js: &js{nc: jsc.nc, opts: o}}
	go func() {
		if cancel != nil {
			defer cancel()
		}
		defer close(ch)
		for l.Next() {
			for _, info := range l.Page() {
				select {
				case ch <- info:
				case <-o.ctx.Done():
					return
				}
			}
		}
	}()

	return ch
}

func getJSContextOpts(defs *jsOpts, opts ...JSOpt) (*jsOpts, context.CancelFunc, error) {
	var o jsOpts
	for _, opt := range opts {
		if err := opt.configureJSContext(&o); err != nil {
			return nil, nil, err
		}
	}

	// Check for option collisions. Right now just timeout and context.
	if o.ctx != nil && o.wait != 0 {
		return nil, nil, ErrContextAndTimeout
	}
	if o.wait == 0 && o.ctx == nil {
		o.wait = defs.wait
	}
	var cancel context.CancelFunc
	if o.ctx == nil && o.wait > 0 {
		o.ctx, cancel = context.WithTimeout(context.Background(), o.wait)
	}
	if o.pre == "" {
		o.pre = defs.pre
	}

	return &o, cancel, nil
}
