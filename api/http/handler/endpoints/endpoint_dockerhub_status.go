package endpoints

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	httperror "github.com/portainer/libhttp/error"
	"github.com/portainer/libhttp/request"
	"github.com/portainer/libhttp/response"
	portainer "github.com/portainer/portainer/api"
	bolterrors "github.com/portainer/portainer/api/bolt/errors"
	"github.com/portainer/portainer/api/http/client"
)

const (
	tokenURL      = "https://auth.docker.io/token?service=registry.docker.io&scope=repository:ratelimitpreview/test:pull"
	rateLimitsURL = "https://registry-1.docker.io/v2/ratelimitpreview/test/manifests/latest"
)

type dockerhubStatusResponse struct {
	Remaining int `json:"remaining"`
	Limit     int `json:"limit"`
}

// GET request on /api/endpoints/{id}/dockerhub/status
func (handler *Handler) endpointDockerhubStatus(w http.ResponseWriter, r *http.Request) *httperror.HandlerError {
	endpointID, err := request.RetrieveNumericRouteVariableValue(r, "id")
	if err != nil {
		return &httperror.HandlerError{http.StatusBadRequest, "Invalid endpoint identifier route variable", err}
	}

	endpoint, err := handler.DataStore.Endpoint().Endpoint(portainer.EndpointID(endpointID))
	if err == bolterrors.ErrObjectNotFound {
		return &httperror.HandlerError{http.StatusNotFound, "Unable to find an endpoint with the specified identifier inside the database", err}
	} else if err != nil {
		return &httperror.HandlerError{http.StatusInternalServerError, "Unable to find an endpoint with the specified identifier inside the database", err}
	}

	if !strings.HasPrefix(endpoint.URL, "unix://") && !strings.HasPrefix(endpoint.URL, "npipe://") && endpoint.Type != portainer.KubernetesLocalEnvironment {
		return &httperror.HandlerError{http.StatusBadRequest, "Invalid environment type", errors.New("Invalid environment type")}
	}

	dockerhub, err := handler.DataStore.DockerHub().DockerHub()
	if err != nil {
		return &httperror.HandlerError{http.StatusInternalServerError, "Unable to retrieve DockerHub details from the database", err}
	}

	httpClient := client.NewHTTPClient()
	token, err := getDockerHubToken(httpClient, dockerhub)
	if err != nil {
		return &httperror.HandlerError{http.StatusInternalServerError, "Unable to retrieve DockerHub token from DockerHub", err}
	}

	log.Printf("[DEBUG] [http,endpoints,dockerhub] [token: %s] [message: received dockerhub token]", token)

	resp, err := getDockerHubLimits(httpClient, token)
	if err != nil {
		return &httperror.HandlerError{http.StatusInternalServerError, "Unable to retrieve DockerHub rate limits from DockerHub", err}
	}

	return response.JSON(w, resp)
}

func getDockerHubToken(httpClient *client.HTTPClient, dockerhub *portainer.DockerHub) (string, error) {
	type dockerhubTokenResponse struct {
		Token string `json:"token"`
	}

	req, err := http.NewRequest(http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}

	if dockerhub.Authentication {
		req.SetBasicAuth(dockerhub.Username, dockerhub.Password)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.New("failed fetching dockerhub token")
	}

	var data dockerhubTokenResponse
	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		return "", err
	}

	return data.Token, nil
}

func getDockerHubLimits(httpClient *client.HTTPClient, token string) (*dockerhubStatusResponse, error) {

	req, err := http.NewRequest(http.MethodHead, rateLimitsURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("failed fetching dockerhub limits")
	}

	rateLimit, err := parseNumericHeader(resp.Header, "RateLimit-Limit")
	if err != nil {
		return nil, fmt.Errorf("Failed fetching RateLimit-Limit header: %w", err)
	}

	rateLimitRemaining, err := parseNumericHeader(resp.Header, "RateLimit-Remaining")
	if err != nil {
		return nil, fmt.Errorf("Failed fetching RateLimit-Remaining header: %w", err)
	}

	return &dockerhubStatusResponse{
		Limit:     rateLimit,
		Remaining: rateLimitRemaining,
	}, nil
}

func parseNumericHeader(headers http.Header, headerKey string) (int, error) {
	headerValue := headers.Get(headerKey)
	if headerValue == "" {
		return 0, fmt.Errorf("Missing %s header", headerKey)
	}

	matches := strings.Split(headerValue, ";")
	value, err := strconv.Atoi(matches[0])
	if err != nil {
		return 0, err
	}

	return value, nil
}
