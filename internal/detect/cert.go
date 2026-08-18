package detect

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math"
	"os"
	"time"

	"github.com/s2005lg/net-probe/internal/report"
)

func CertInfo(path string) (*report.Cert, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("no PEM data")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	days := int(math.Round(time.Until(cert.NotAfter).Hours() / 24))
	return &report.Cert{NotAfter: cert.NotAfter.UTC().Format(time.RFC3339), DaysLeft: days}, nil
}
