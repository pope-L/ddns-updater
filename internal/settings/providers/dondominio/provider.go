package dondominio

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/qdm12/ddns-updater/internal/models"
	"github.com/qdm12/ddns-updater/internal/settings/constants"
	"github.com/qdm12/ddns-updater/internal/settings/errors"
	"github.com/qdm12/ddns-updater/internal/settings/headers"
	"github.com/qdm12/ddns-updater/internal/settings/utils"
	"github.com/qdm12/ddns-updater/pkg/publicip/ipversion"
)

type Provider struct {
	domain    string
	host      string
	ipVersion ipversion.IPVersion
	username  string
	password  string
	name      string
}

func New(data json.RawMessage, domain, host string,
	ipVersion ipversion.IPVersion) (p *Provider, err error) {
	extraSettings := struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Name     string `json:"name"`
	}{}
	if err := json.Unmarshal(data, &extraSettings); err != nil {
		return nil, err
	}
	if len(host) == 0 {
		host = "@" // default
	}
	p = &Provider{
		domain:    domain,
		host:      host,
		ipVersion: ipVersion,
		username:  extraSettings.Username,
		password:  extraSettings.Password,
		name:      extraSettings.Name,
	}
	if err := p.isValid(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Provider) isValid() error {
	switch {
	case len(p.username) == 0:
		return errors.ErrEmptyUsername
	case len(p.password) == 0:
		return errors.ErrEmptyPassword
	case len(p.name) == 0:
		return errors.ErrEmptyName
	case p.host != "@":
		return errors.ErrHostOnlyAt
	}
	return nil
}

func (p *Provider) String() string {
	return utils.ToString(p.domain, p.host, constants.DonDominio, p.ipVersion)
}

func (p *Provider) Domain() string {
	return p.domain
}

func (p *Provider) Host() string {
	return p.host
}

func (p *Provider) IPVersion() ipversion.IPVersion {
	return p.ipVersion
}

func (p *Provider) Proxied() bool {
	return false
}

func (p *Provider) BuildDomainName() string {
	return utils.BuildDomainName(p.host, p.domain)
}

func (p *Provider) HTML() models.HTMLRow {
	return models.HTMLRow{
		Domain:    models.HTML(fmt.Sprintf("<a href=\"http://%s\">%s</a>", p.BuildDomainName(), p.BuildDomainName())),
		Host:      models.HTML(p.Host()),
		Provider:  "<a href=\"https://www.dondominio.com/\">DonDominio</a>",
		IPVersion: models.HTML(p.ipVersion.String()),
	}
}

func (p *Provider) setHeaders(request *http.Request) {
	headers.SetUserAgent(request)
	headers.SetContentType(request, "application/x-www-form-urlencoded")
	headers.SetAccept(request, "application/json")
}

func (p *Provider) Update(ctx context.Context, client *http.Client, ip net.IP) (newIP net.IP, err error) {
	u := url.URL{
		Scheme: "https",
		Host:   "simple-api.dondominio.net",
	}
	values := url.Values{}
	values.Set("apiuser", p.username)
	values.Set("apipasswd", p.password)
	values.Set("domain", p.domain)
	values.Set("name", p.name)
	isIPv4 := ip.To4() != nil
	if isIPv4 {
		values.Set("ipv4", ip.String())
	} else {
		values.Set("ipv6", ip.String())
	}
	encodedValues := values.Encode()
	buffer := strings.NewReader(encodedValues)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), buffer)
	if err != nil {
		return nil, err
	}
	p.setHeaders(request)

	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %d: %s",
			errors.ErrBadHTTPStatus, response.StatusCode, utils.BodyToSingleLine(response.Body))
	}

	decoder := json.NewDecoder(response.Body)
	var responseData struct {
		Success          bool   `json:"success"`
		ErrorCode        int    `json:"errorCode"`
		ErrorCodeMessage string `json:"errorCodeMsg"`
		ResponseData     struct {
			GlueRecords []struct {
				IPv4 string `json:"ipv4"`
				IPv6 string `json:"ipv6"`
			} `json:"gluerecords"`
		} `json:"responseData"`
	}
	if err := decoder.Decode(&responseData); err != nil {
		return nil, fmt.Errorf("%w: %s", errors.ErrUnmarshalResponse, err)
	}

	if !responseData.Success {
		return nil, fmt.Errorf("%w: %s (error code %d)",
			errors.ErrUnsuccessfulResponse, responseData.ErrorCodeMessage, responseData.ErrorCode)
	}
	ipString := responseData.ResponseData.GlueRecords[0].IPv4
	if !isIPv4 {
		ipString = responseData.ResponseData.GlueRecords[0].IPv6
	}
	newIP = net.ParseIP(ipString)
	if newIP == nil {
		return nil, fmt.Errorf("%w: %s", errors.ErrIPReceivedMalformed, ipString)
	} else if !ip.Equal(newIP) {
		return nil, fmt.Errorf("%w: %s", errors.ErrIPReceivedMismatch, newIP.String())
	}
	return newIP, nil
}
