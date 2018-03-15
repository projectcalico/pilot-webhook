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
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/docopt/docopt-go"
	"github.com/emicklei/go-restful"
	log "github.com/sirupsen/logrus"
	"istio.io/istio/pilot/pkg/proxy/envoy/v1"
)

const usage = `Istio Pilot Webhook

Usage:
  webhook <path> [options]

Options:
  <path>                 Absolute path to webhook listen socket
  --debug                Log at Debug level.
  --sendcluster          Send cluster information.`

const version = "0.1"

const serviceNodeSeparator = "~"
const listenerNameSeparator = "_"
const AuthZFilterName = "envoy.ext_authz"
const AuthZClusterName = "calico.dikastes"
const DikastesSocketDir = "/var/run/dikastes"

type ldsResponse struct {
	Listeners v1.Listeners `json:"listeners"`
}

type cdsResponse struct {
	Clusters v1.Clusters `json:"clusters"`
}

type Direction int

const (
	INBOUND Direction = iota
	OUTBOUND
	VIRTUAL
)

type Protocol int

const (
	HTTP Protocol = iota
	TCP
)

type AuthzFilterConfig struct {
	StatPrefix  string             `json:"stat_prefix,omitempty"`
	GrpcCluster *GrpcClusterConfig `json:"grpc_cluster,omitempty"`
}

type GrpcClusterConfig struct {
	ClusterName string `json:"cluster_name"`
	// TODO: (spikecurtis) include Duration once we move to v2 API.
}

type options struct {
	SetCluster bool
}

var configOptions options

func (*AuthzFilterConfig) IsNetworkFilterConfig() {}

func main() {
	arguments, err := docopt.Parse(usage, nil, true, version, false)
	if err != nil {
		println(usage)
		return
	}
	if arguments["--debug"].(bool) {
		log.SetLevel(log.DebugLevel)
	}

	if arguments["--sendcluster"].(bool) {
		configOptions.SetCluster = true
	}

	ws := newWebhook()
	restful.Add(ws)

	filePath := arguments["<path>"].(string)
	lis := openSocket(filePath)
	defer lis.Close()

	server := http.Server{}
	log.Fatal(server.Serve(lis))
}

// newWebhook creates a WebService with the xDS webhook routes
func newWebhook() *restful.WebService {
	ws := new(restful.WebService)
	ws.Route(ws.POST("/v1/listeners/{serviceCluster}/{serviceNode}").
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON).
		To(listeners))
	ws.Route(ws.POST("/v1/clusters/{serviceCluster}/{serviceNode}").
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON).
		To(clusters))
	ws.Route(ws.POST("/v1/routes/{routeConfigName}/{serviceCluster}/{serviceNode}").
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON).
		To(routes))
	ws.Route(ws.POST("/v1/registration/{serviceName}").
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON).
		To(endpoints))
	return ws
}

// openSocket opens a Unix Domain Socket listening on the given filePath
func openSocket(filePath string) net.Listener {
	_, err := os.Stat(filePath)
	if !os.IsNotExist(err) {
		// file exists, try to delete it.
		err := os.Remove(filePath)
		if err != nil {
			log.WithFields(log.Fields{
				"listen": filePath,
				"err":    err,
			}).Fatal("File exists and unable to remove.")
		}
	}
	lis, err := net.Listen("unix", filePath)
	if err != nil {
		log.WithFields(log.Fields{
			"listen": filePath,
			"err":    err,
		}).Fatal("Unable to listen.")
	}
	err = os.Chmod(filePath, 0777)
	// Anyone on system can connect.
	if err != nil {
		log.Fatal("Unable to set write permission on socket.")
	}
	return lis
}

// listeners handles LDS hooks and inserts the external authz filter
func listeners(req *restful.Request, resp *restful.Response) {
	serviceNode := req.PathParameter("serviceNode")
	c := strings.Split(serviceNode, serviceNodeSeparator)
	nodeType := c[0]
	ip := c[1]
	if nodeType != "sidecar" {
		// Return unmodified.
		io.Copy(resp, req.Request.Body)
		return
	}
	body, err := ioutil.ReadAll(req.Request.Body)
	if err != nil {
		log.Error("failed to read")
		resp.WriteErrorString(http.StatusInternalServerError, "failed to read request")
		return
	}
	var lds ldsResponse
	err = json.Unmarshal(body, &lds)
	if err != nil {
		log.WithField("err", err).Error("failed to decode JSON")
		fmt.Print(string(body))
		resp.WriteErrorString(http.StatusBadRequest, "could not parse request JSON")
		return
	}
	for _, l := range lds.Listeners {
		updateListener(l, ip)
	}
	out, err := json.Marshal(lds)
	if err != nil {
		log.WithField("err", err).Error("failed to re-encode")
		resp.WriteErrorString(http.StatusInternalServerError, "internal error")
		return
	}
	resp.Write(out)
	return
}

// updateListener processes a single Listener struct and inserts the external authz filter on inbound listeners.
func updateListener(listener *v1.Listener, ip string) {
	direction, proto := classifyListener(listener, ip)

	// We only care about inbound listeners
	if direction == OUTBOUND {
		log.WithField("name", listener.Name).Debug("Skipping outbound listener")
		return
	} else if direction == VIRTUAL {
		log.Debug("Skipping virtual listener")
		return
	}
	switch proto {
	case HTTP:
		updateHTTPListener(listener)
	case TCP:
		updateTCPListener(listener)
	}
}

// classifyListener determines whether the listener is (inbound|outbound|virtual) and whether it is http or tcp protocol
func classifyListener(listener *v1.Listener, ip string) (Direction, Protocol) {
	var proto Protocol
	if listener.Name == "virtual" {
		return VIRTUAL, proto
	}
	c := strings.Split(listener.Name, listenerNameSeparator)
	if c[0] == "http" {
		proto = HTTP
	} else if c[0] == "tcp" {
		proto = TCP
	}
	if c[1] == ip {
		return INBOUND, proto
	} else {
		return OUTBOUND, proto
	}
}

// updateHTTPListener inserts the external authz filter into the HTTP connection manager
func updateHTTPListener(listener *v1.Listener) {
	log.WithField("name", listener.Name).Debug("Updating HTTP listener")
	var httpManagerConfig v1.NetworkFilterConfig
	for _, filter := range listener.Filters {
		if filter.Name == v1.HTTPConnectionManager {
			httpManagerConfig = filter.Config
			break
		}
	}
	if httpManagerConfig != nil {
		// Found HTTP Listener
		cfg := httpManagerConfig.(*v1.HTTPFilterConfig)
		// Prepend; it must be the first filter so a failed authorization will close the connection.
		authzHttp := v1.HTTPFilter{
			Type:   "decoder",
			Name:   AuthZFilterName,
			Config: &AuthzFilterConfig{GrpcCluster: &GrpcClusterConfig{ClusterName: AuthZClusterName}},
		}
		cfg.Filters = append([]v1.HTTPFilter{authzHttp}, cfg.Filters...)
	} else {
		log.WithField("listener", *listener).Error("tried to add HTTP Authz filter to non-HTTP listener")
	}
	return
}

// updateTCPListener adds the external authz network filter
func updateTCPListener(listener *v1.Listener) {
	log.WithField("name", listener.Name).Debug("Updating TCP listener")
	authzTCP := v1.NetworkFilter{
		Type: "read",
		Name: AuthZFilterName,
		Config: &AuthzFilterConfig{StatPrefix: AuthZFilterName,
			GrpcCluster: &GrpcClusterConfig{ClusterName: AuthZClusterName}},
	}
	// Prepend; it must be the first filter so a failed authorization will close the connection.
	listener.Filters = append([]*v1.NetworkFilter{&authzTCP}, listener.Filters...)
	return
}

// clusters handles the CDS hook and inserts the dikastes cluster
func clusters(req *restful.Request, resp *restful.Response) {
	// TODO(saumoh): when we don't change the cluster body (configOptions.SetCluster == false)
	// we should just do a io.Copy(resp, req.Request.Body)
	// but that results in Envoy rejecting the configuration.
	// Hence, read, deconstruct, no-op, write to output!
	body, err := ioutil.ReadAll(req.Request.Body)
	if err != nil {
		log.Error("failed to read")
		return
	}
	var cds cdsResponse
	err = json.Unmarshal(body, &cds)
	if err != nil {
		log.WithField("err", err).Error("failed to decode JSON")
		resp.WriteErrorString(http.StatusBadRequest, "could not parse request JSON")
		return
	}
	if configOptions.SetCluster {
		cds.Clusters = append(cds.Clusters, &v1.Cluster{
			Name:             AuthZClusterName,
			ConnectTimeoutMs: 5000,
			Type:             v1.ClusterTypeStatic,
			CircuitBreaker: &v1.CircuitBreaker{
				Default: v1.DefaultCBPriority{
					MaxPendingRequests: 10000,
					MaxRequests:        10000,
				},
			},
			LbType:   v1.LbTypeRoundRobin,
			Features: v1.ClusterFeatureHTTP2,
			Hosts:    []v1.Host{{URL: "unix://" + DikastesSocketDir + "/dikastes.sock"}},
		})
	}
	out, err := json.Marshal(cds)
	if err != nil {
		log.WithField("err", err).Error("failed to re-encode")
		return
	}
	resp.Write(out)
}

// routes handles the RDS hook and is a passthru
func routes(req *restful.Request, resp *restful.Response) {
	io.Copy(resp, req.Request.Body)
}

// endpoints handles the EDS hook and is a passthru
func endpoints(req *restful.Request, resp *restful.Response) {
	io.Copy(resp, req.Request.Body)
}
