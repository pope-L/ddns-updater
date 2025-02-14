package namecheap

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"github.com/qdm12/ddns-updater/internal/models"
	"github.com/qdm12/ddns-updater/internal/settings/common"
	"github.com/qdm12/ddns-updater/internal/settings/constants"
	"github.com/qdm12/ddns-updater/internal/settings/errors"
	"github.com/qdm12/ddns-updater/internal/settings/headers"
	"github.com/qdm12/ddns-updater/internal/settings/utils"
	"github.com/qdm12/ddns-updater/pkg/publicip/ipversion"
)

type Provider struct {
	domain        string
	host          string
	ipVersion     ipversion.IPVersion
	password      string
	useProviderIP bool
	matcher       common.Matcher
}

func New(data json.RawMessage, domain, host string, ipVersion ipversion.IPVersion,
	matcher common.Matcher) (p *Provider, err error) {
	if ipVersion == ipversion.IP6 {
		return p, errors.ErrIPv6NotSupported
	}
	extraSettings := struct {
		Password      string `json:"password"`
		UseProviderIP bool   `json:"provider_ip"`
	}{}
	if err := json.Unmarshal(data, &extraSettings); err != nil {
		return nil, err
	}
	p = &Provider{
		domain:        domain,
		host:          host,
		ipVersion:     ipVersion,
		password:      extraSettings.Password,
		useProviderIP: extraSettings.UseProviderIP,
		matcher:       matcher,
	}
	if err := p.isValid(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Provider) isValid() error {
	if !p.matcher.NamecheapPassword(p.password) {
		return errors.ErrMalformedPassword
	}
	return nil
}

func (p *Provider) String() string {
	return utils.ToString(p.domain, p.host, constants.Namecheap, p.ipVersion)
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
		Provider:  "<a href=\"https://namecheap.com\">Namecheap</a>",
		IPVersion: models.HTML(p.ipVersion.String()),
	}
}

func (p *Provider) setHeaders(request *http.Request) {
	headers.SetUserAgent(request)
	headers.SetAccept(request, "application/xml")
}

func (p *Provider) Update(ctx context.Context, client *http.Client, ip net.IP) (newIP net.IP, err error) {
	u := url.URL{
		Scheme: "https",
		Host:   "dynamicdns.park-your-domain.com",
		Path:   "/update",
	}
	values := url.Values{}
	values.Set("host", p.host)
	values.Set("domain", p.domain)
	values.Set("password", p.password)
	if !p.useProviderIP {
		values.Set("ip", ip.String())
	}
	u.RawQuery = values.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
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

	decoder := xml.NewDecoder(response.Body)
	decoder.CharsetReader = func(encoding string, input io.Reader) (io.Reader, error) {
		return input, nil
	}

	var parsedXML struct {
		Errors struct {
			Error string `xml:"errors.Err1"`
		} `xml:"errors"`
		IP string `xml:"IP"`
	}
	if err := decoder.Decode(&parsedXML); err != nil {
		return nil, fmt.Errorf("%w: %s", errors.ErrUnmarshalResponse, err)
	}

	if parsedXML.Errors.Error != "" {
		return nil, fmt.Errorf("%w: %s", errors.ErrUnsuccessfulResponse, parsedXML.Errors.Error)
	}

	if parsedXML.IP == "" {
		// If XML has not IP address, just return the IP we sent.
		newIP = ip
		return newIP, nil
	}

	newIP = net.ParseIP(parsedXML.IP)
	if newIP == nil {
		return nil, fmt.Errorf("%w: %s", errors.ErrIPReceivedMalformed, parsedXML.IP)
	}
	if ip != nil && !ip.Equal(newIP) {
		return nil, fmt.Errorf("%w: %s", errors.ErrIPReceivedMismatch, newIP.String())
	}
	return newIP, nil
}
