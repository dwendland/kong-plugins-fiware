package main

import (
	"bytes"
	"fmt"
	"net/http"
	"time"

	cache "github.com/patrickmn/go-cache"
	log "github.com/sirupsen/logrus"
)

type ExtAuthzPDP struct{}

type ExtAuthzRequest struct {
	AccessToken   string
	RequestUrl    string
	RequestAction string
	RequestBody   []byte
}

// decision cache used by extAuthz
var extAuthzCache *cache.Cache

// is caching enabled?
var extAuthzCacheEnabled bool = true

func (ExtAuthzPDP) Authorize(conf *Config, requestInfo *RequestInfo) (decision *bool) {

	// false until proven otherwise.
	decision = getNegativeDecision()

	if conf.ExtAuthz == (ExtAuthzConfig{}) {
		log.Warnf("[ExtAuthz] Invalid config: %v", *conf)
		return decision
	}
	extAuthzConfig := conf.ExtAuthz
	log.Debugf("[ExtAuthz] Using configuration for extern-authz: %v", extAuthzConfig)
	if extAuthzConfig.PDPHost == "" {
		log.Warn("[ExtAuthz] No PDP host was specified.")
		return decision
	}

	var endpointAddress string
	if extAuthzConfig.PDPAuthzPath == "" {
		// default pdp path
		endpointAddress = extAuthzConfig.PDPHost + "/authz"
	} else {
		endpointAddress = extAuthzConfig.PDPHost + extAuthzConfig.PDPAuthzPath
	}
	log.Debugf("[ExtAuthz] PDP address is %s", endpointAddress)

	// remove bearer prefix
	authHeader := cleanAuthHeader(requestInfo.AuthorizationHeader)
	// build the cache key and check if a decision is available
	extAuthzRequest := ExtAuthzRequest{AccessToken: authHeader, RequestUrl: requestInfo.Path, RequestAction: requestInfo.Method, RequestBody: requestInfo.Body}
	cacheKey := fmt.Sprint(extAuthzRequest)
	if extAuthzCache == nil {
		initExtAuthzCache(conf)
	}
	var exists bool = false
	if extAuthzCacheEnabled {
		_, exists = keyrockDecisionCache.Get(cacheKey)
	}

	if exists {
		log.Infof("[ExtAuthz] Found cached decision.")
		// we only cache success, thus dont care about the cache value
		return getPositveDecision()
	}

	authzRequest, err := http.NewRequest(http.MethodPost, endpointAddress, bytes.NewBuffer(extAuthzRequest.RequestBody))
	if err != nil {
		log.Warn("[ExtAuthz] Was not able to build request for the ext-authz service.", err)
		return decision
	}
	authzRequest.Header.Add("X-Original-URI", extAuthzRequest.RequestUrl)
	authzRequest.Header.Add("X-Original-Action", extAuthzRequest.RequestAction)
	// we do the clean and rebuild for bearer to have security about the format. Performance loss is negligible.
	authzRequest.Header.Add("Authorization", "Bearer "+extAuthzRequest.AccessToken)

	// request a decision from the pdp
	response, err := authorizationHttpClient.Do(authzRequest)
	if err != nil {
		log.Errorf("[ExtAuthz] Was not able to call authorization endpoint. Err: %v", err)
		return decision
	}
	if response.StatusCode != 200 {
		log.Errorf("[ExtAuthz] Did not receive a successfull response. Status: %v, Message: %s", response.StatusCode, response.Body)
		return decision
	}

	log.Debugf("[ExtAuthz] Successfully authorized the request.")
	if extAuthzCacheEnabled {
		extAuthzCache.Add(cacheKey, true, cache.DefaultExpiration)
	}
	return getPositveDecision()

}

func initExtAuthzCache(config *Config) {
	var expiry = config.DecisionCacheExpiryInS
	if expiry == -1 {
		log.Infof("[ExtAuthz] Decision caching is disabled.")
		extAuthzCacheEnabled = false
		return
	}
	if expiry == 0 {
		log.Infof("[ExtAuthz] Use default expiry of %vs.", DefaultExpiry)
		expiry = DefaultExpiry
	}
	extAuthzCache = cache.New(time.Duration(expiry)*time.Second, time.Duration(2*expiry)*time.Second)
}
