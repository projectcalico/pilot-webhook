// Copyright (c) 2018 Tigera, Inc. All rights reserved.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"testing"

	"strings"

	"net/http"

	"github.com/emicklei/go-restful"
	. "github.com/onsi/gomega"
	"istio.io/istio/pilot/pkg/proxy/envoy/v1"
)

const (
	NODE_IP         = "3.4.5.6"
	SERVICE_CLUSTER = "testcluster"
	ROUTE_CONFIG    = "testrds"
	SERVICE_NAME    = "testeds"
)

func serviceNode(nodeType, ip string) string {
	return fmt.Sprintf("%s~%s~other~items", nodeType, ip)
}

func newLDSRequest(nodeType string, body io.Reader) *restful.Request {
	sn := serviceNode(nodeType, NODE_IP)
	url := fmt.Sprintf("http://unix/v1/listeners/%s/%s", SERVICE_CLUSTER, sn)
	httpReq := httptest.NewRequest("POST", url, body)
	req := restful.NewRequest(httpReq)
	req.PathParameters()["serviceNode"] = sn
	return req
}

func newCDSRequest(nodeType string, body io.Reader) *restful.Request {
	sn := serviceNode(nodeType, NODE_IP)
	url := fmt.Sprintf("http://unix/v1/clusters/%s/%s", SERVICE_CLUSTER, sn)
	httpReq := httptest.NewRequest("POST", url, body)
	req := restful.NewRequest(httpReq)
	req.PathParameters()["serviceNode"] = sn
	return req
}

func newRDSRequest(nodeType string, body io.Reader) *restful.Request {
	sn := serviceNode(nodeType, NODE_IP)
	url := fmt.Sprintf("http://unix/v1/routes/%s/%s/%s", ROUTE_CONFIG, SERVICE_CLUSTER, sn)
	httpReq := httptest.NewRequest("POST", url, body)
	req := restful.NewRequest(httpReq)
	req.PathParameters()["serviceNode"] = sn
	return req
}

func newEDSRequest(body io.Reader) *restful.Request {
	url := fmt.Sprintf("http://unix/v1/registration/%s", SERVICE_NAME)
	httpReq := httptest.NewRequest("POST", url, body)
	req := restful.NewRequest(httpReq)
	return req
}

func TestListenersMainline(t *testing.T) {
	RegisterTestingT(t)

	ldsReq := ldsResponse{Listeners: []*v1.Listener{
		{
			Name: "http_0.0.0.0_80",
		},
		{
			Name: "http_" + NODE_IP + "_43",
			Filters: []*v1.NetworkFilter{
				{
					Name: v1.HTTPConnectionManager,
					Config: &v1.HTTPFilterConfig{
						Filters: []v1.HTTPFilter{
							{
								Name: v1.CORSFilter,
							},
						},
					},
				},
			},
		},
	}}
	ldsBytes, err := json.Marshal(ldsReq)
	Expect(err).To(BeNil())
	req := newLDSRequest("sidecar", bytes.NewReader(ldsBytes))
	recorder := httptest.NewRecorder()
	resp := restful.NewResponse(recorder)
	listeners(req, resp)
	var ldsResp ldsResponse
	err = json.Unmarshal(recorder.Body.Bytes(), &ldsResp)
	Expect(err).To(BeNil())
	Expect(ldsReq.Listeners[0]).To(Equal(ldsResp.Listeners[0]))
	hcm := ldsResp.Listeners[1].Filters[0].Config.(*v1.HTTPFilterConfig)
	Expect(len(hcm.Filters)).To(Equal(2))
	Expect(hcm.Filters[0].Name).To(Equal(AuthZFilterName))
}

func TestListenersBadReq(t *testing.T) {
	RegisterTestingT(t)

	req := newLDSRequest("sidecar", strings.NewReader("not JSON"))
	recorder := httptest.NewRecorder()
	resp := restful.NewResponse(recorder)
	listeners(req, resp)
	Expect(recorder.Code).To(Equal(http.StatusBadRequest))
}

func TestListenersNotSidecar(t *testing.T) {
	RegisterTestingT(t)

	reqString := `{"listeners": []}`
	req := newLDSRequest("ingress", strings.NewReader(reqString))
	recorder := httptest.NewRecorder()
	resp := restful.NewResponse(recorder)
	listeners(req, resp)
	Expect(recorder.Body.String()).To(Equal(reqString))
	Expect(recorder.Code).To(Equal(http.StatusOK))
}

func TestUpdateListenersSkipped(t *testing.T) {
	testCases := []struct {
		Title    string
		Listener v1.Listener
	}{
		{
			Title:    "Outbound",
			Listener: v1.Listener{Name: "http_10.65.8.9_443"},
		},
		{
			Title:    "Virtual",
			Listener: v1.Listener{Name: "virtual"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.Title, func(t *testing.T) {
			RegisterTestingT(t)
			l := tc.Listener
			updateListener(&l, "1.2.3.4")
			Expect(l).To(Equal(tc.Listener))
		})
	}
}

func TestUpdateListenersTCP(t *testing.T) {
	RegisterTestingT(t)

	l := v1.Listener{
		Name:    "tcp_1.2.3.4_76",
		Filters: []*v1.NetworkFilter{{Name: v1.TCPProxyFilter}},
	}
	updateListener(&l, "1.2.3.4")
	Expect(len(l.Filters)).To(Equal(2))
	Expect(l.Filters[0].Name).To(Equal(AuthZFilterName))
}

func TestClustersMainline(t *testing.T) {
	RegisterTestingT(t)

	cdsReq := cdsResponse{Clusters: []*v1.Cluster{
		{
			Name: "out.something.com|http",
		},
	}}
	cdsBytes, err := json.Marshal(cdsReq)
	Expect(err).To(BeNil())
	req := newCDSRequest("sidecar", bytes.NewReader(cdsBytes))
	recorder := httptest.NewRecorder()
	resp := restful.NewResponse(recorder)
	clusters(req, resp)
	var cdsResp cdsResponse
	err = json.Unmarshal(recorder.Body.Bytes(), &cdsResp)
	Expect(err).To(BeNil())
	Expect(len(cdsResp.Clusters)).To(Equal(len(cdsReq.Clusters) + 1))
	Expect(cdsResp.Clusters[1].Name).To(Equal(AuthZClusterName))
}

func TestClustersBadReq(t *testing.T) {
	RegisterTestingT(t)

	req := newCDSRequest("sidecar", strings.NewReader("not JSON"))
	recorder := httptest.NewRecorder()
	resp := restful.NewResponse(recorder)
	clusters(req, resp)
	Expect(recorder.Code).To(Equal(http.StatusBadRequest))
}

func TestRoutesPassthru(t *testing.T) {
	RegisterTestingT(t)

	body := "testing RDS"
	req := newRDSRequest("sidecar", strings.NewReader(body))
	rec := httptest.NewRecorder()
	resp := restful.NewResponse(rec)
	routes(req, resp)
	Expect(rec.Code).To(Equal(http.StatusOK))
	Expect(rec.Body.String()).To(Equal(body))
}

func TestEndpointsPassthru(t *testing.T) {
	RegisterTestingT(t)

	body := "testing EDS"
	req := newEDSRequest(strings.NewReader(body))
	rec := httptest.NewRecorder()
	resp := restful.NewResponse(rec)
	endpoints(req, resp)
	Expect(rec.Code).To(Equal(http.StatusOK))
	Expect(rec.Body.String()).To(Equal(body))
}
