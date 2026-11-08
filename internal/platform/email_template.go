package platform

import (
	"fmt"
	htmltemplate "html/template"
	"strings"
	texttemplate "text/template"
	"time"
)

// EmailCodeData is the presentation model for the one-time code email.
type EmailCodeData struct {
	Brand        string
	Code         string
	Digits       []string
	Purpose      string
	PurposeLabel string
	Email        string
	TTLMinutes   int
	RequestIP    string
	SupportEmail string
	Year         int
}

// RenderEmailCode renders the HTML and plain-text body. The HTML is a
// responsive, Ozon-style transactional email with the code in large boxes;
// every value is auto-escaped by html/template.
func RenderEmailCode(data EmailCodeData) (subject, html, text string, err error) {
	if data.Brand == "" {
		data.Brand = "GetContact"
	}
	if data.PurposeLabel == "" {
		data.PurposeLabel = purposeLabel(data.Purpose)
	}
	if data.TTLMinutes <= 0 {
		data.TTLMinutes = 10
	}
	if data.Year == 0 {
		data.Year = time.Now().Year()
	}
	data.Digits = nil
	for _, r := range data.Code {
		data.Digits = append(data.Digits, string(r))
	}

	subject = "Код подтверждения " + data.Brand
	if data.Purpose == "login" {
		subject = "Код входа в " + data.Brand
	}

	var out strings.Builder
	if err := codeHTMLTemplate.Execute(&out, data); err != nil {
		return "", "", "", fmt.Errorf("render html template: %w", err)
	}
	var plain strings.Builder
	if err := codeTextTemplate.Execute(&plain, data); err != nil {
		return "", "", "", fmt.Errorf("render text template: %w", err)
	}
	return subject, out.String(), plain.String(), nil
}

func purposeLabel(purpose string) string {
	switch purpose {
	case "login":
		return "вход в аккаунт"
	case "signup":
		return "подтверждение почты"
	case "verify_email":
		return "подтверждение почты"
	default:
		return "подтверждение действия"
	}
}

var codeHTMLTemplate = htmltemplate.Must(htmltemplate.New("email_code").Parse(`<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light">
<title>{{.Brand}} — код подтверждения</title>
</head>
<body style="margin:0;padding:0;background:#eef1f7;-webkit-text-size-adjust:100%;">
<div style="display:none;max-height:0;overflow:hidden;opacity:0;color:transparent;">
Ваш код {{.Brand}}: {{.Code}}. Код действует {{.TTLMinutes}} минут.
</div>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#eef1f7;padding:32px 12px;">
  <tr>
    <td align="center">
      <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="max-width:560px;background:#ffffff;border-radius:24px;overflow:hidden;box-shadow:0 18px 40px rgba(24,32,64,.12);font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;">
        <tr>
          <td style="background:linear-gradient(135deg,#005bff 0%,#7b2ff7 55%,#b02ee8 100%);padding:30px 32px 26px 32px;">
            <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0">
              <tr>
                <td style="font-size:22px;line-height:1;font-weight:800;letter-spacing:-.5px;color:#ffffff;">
                  <span style="display:inline-block;width:34px;height:34px;line-height:34px;text-align:center;border-radius:11px;background:rgba(255,255,255,.2);margin-right:10px;font-size:18px;">G</span>{{.Brand}}
                </td>
                <td align="right" style="font-size:12px;color:rgba(255,255,255,.78);letter-spacing:.4px;text-transform:uppercase;">Безопасность</td>
              </tr>
            </table>
          </td>
        </tr>
        <tr>
          <td style="padding:36px 36px 8px 36px;">
            <div style="font-size:13px;font-weight:700;color:#005bff;letter-spacing:.8px;text-transform:uppercase;margin-bottom:12px;{{if ne .Purpose "login"}}color:#7b2ff7;{{end}}">{{.PurposeLabel}}</div>
            <h1 style="margin:0 0 12px 0;font-size:28px;line-height:1.22;font-weight:800;color:#161a2e;letter-spacing:-.4px;">Ваш код подтверждения</h1>
            <p style="margin:0;font-size:15px;line-height:1.65;color:#5a6178;">
              Введите этот код, чтобы подтвердить, что адрес <strong style="color:#161a2e;">{{.Email}}</strong> принадлежит вам. Никому его не сообщайте.
            </p>
          </td>
        </tr>
        <tr>
          <td style="padding:26px 36px 0 36px;">
            <table role="presentation" cellpadding="0" cellspacing="0" border="0" align="center" style="margin:0 auto;background:#f4f6ff;border:1px solid #e3e8ff;border-radius:18px;padding:16px 18px;">
              <tr>
                {{range .Digits}}
                <td style="padding:0 5px;">
                  <div style="width:46px;height:58px;line-height:58px;text-align:center;background:#ffffff;border:1px solid #d9e0ff;border-radius:12px;font-size:30px;font-weight:800;color:#161a2e;font-family:'SF Mono',Consolas,'Liberation Mono',monospace;box-shadow:0 2px 6px rgba(24,32,64,.06);">{{.}}</div>
                </td>
                {{end}}
              </tr>
            </table>
            <p style="margin:18px 0 0 0;text-align:center;font-size:13px;color:#8a90a5;">
              Код действует <strong style="color:#5a6178;">{{.TTLMinutes}} минут</strong>. Запросить новый можно после истечения.
            </p>
          </td>
        </tr>
        <tr>
          <td style="padding:30px 36px 0 36px;">
            <table role="presentation" width="100%" cellpadding="0" cellspacing="0" border="0" style="background:#fff8ec;border:1px solid #ffe3b8;border-radius:16px;">
              <tr>
                <td style="padding:16px 18px;font-size:13px;line-height:1.6;color:#7a5a1d;">
                  <strong style="color:#5f4310;">Если вы не запрашивали код</strong><br>
                  просто проигнорируйте это письмо. Аккаунт останется в безопасности, а код скоро перестанет действовать.
                  {{if .RequestIP}}<br><span style="color:#a08a62;">IP запроса: {{.RequestIP}}</span>{{end}}
                </td>
              </tr>
            </table>
          </td>
        </tr>
        <tr>
          <td style="padding:30px 36px 36px 36px;">
            <div style="height:1px;background:#edf0f7;margin-bottom:22px;"></div>
            <p style="margin:0 0 8px 0;font-size:13px;line-height:1.6;color:#8a90a5;">
              Это автоматическое письмо — отвечать на него не нужно. Если нужна помощь, напишите на
              <a href="mailto:{{.SupportEmail}}" style="color:#005bff;text-decoration:none;">{{.SupportEmail}}</a>.
            </p>
            <p style="margin:0;font-size:12px;line-height:1.6;color:#a7adbe;">
              © {{.Year}} {{.Brand}}. Безопасность и приватность по умолчанию.
            </p>
          </td>
        </tr>
      </table>
      <div style="max-width:560px;margin:16px auto 0 auto;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:11px;line-height:1.6;color:#9aa0b2;text-align:center;padding:0 18px;">
        Никогда не пересылайте код третьим лицам. Сотрудники {{.Brand}} не спрашивают код подтверждения.
      </div>
    </td>
  </tr>
</table>
</body>
</html>
`))

var codeTextTemplate = texttemplate.Must(texttemplate.New("email_code_text").Parse(`{{.Brand}} — {{.PurposeLabel}}

Ваш код подтверждения: {{.Code}}

Введите этот код, чтобы подтвердить, что адрес {{.Email}} принадлежит вам.
Код действует {{.TTLMinutes}} минут. Никому его не сообщайте.

Если вы не запрашивали код, просто проигнорируйте это письмо.
{{if .RequestIP}}IP запроса: {{.RequestIP}}{{end}}

Это автоматическое письмо. Помощь: {{.SupportEmail}}
© {{.Year}} {{.Brand}}
`))
