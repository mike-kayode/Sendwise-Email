package identity

import "html"

func verificationEmailHTML(name, link, baseURL string) string {
	return emailShell(
		"Verify your email address",
		"Hi "+html.EscapeString(name)+",",
		"Confirm your email address to activate your Sendwise workspace.",
		"Verify email", link,
		"If you did not create an account, you can safely ignore this message.",
		baseURL,
	)
}

func resetEmailHTML(name, link, baseURL string) string {
	return emailShell(
		"Reset your password",
		"Hi "+html.EscapeString(name)+",",
		"We received a request to reset your password. This link expires in one hour.",
		"Reset password", link,
		"If you did not request a password reset, you can safely ignore this message.",
		baseURL,
	)
}

func emailShell(subject, greeting, body, cta, link, footer, baseURL string) string {
	return `<!doctype html>
<html lang="en">
  <body style="margin:0;background:#f6f8f7;font-family:Helvetica,Arial,sans-serif;">
    <table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f6f8f7;padding:24px 0;">
      <tr><td align="center">
        <table role="presentation" width="560" cellpadding="0" cellspacing="0" style="max-width:560px;width:100%;background:#ffffff;border-radius:12px;padding:32px;">
          <tr><td style="font-size:18px;font-weight:700;color:#0f766e;padding-bottom:16px;">Sendwise</td></tr>
          <tr><td style="font-size:20px;font-weight:700;color:#0f172a;padding-bottom:12px;">` + html.EscapeString(subject) + `</td></tr>
          <tr><td style="font-size:15px;color:#334155;padding-bottom:8px;">` + greeting + `</td></tr>
          <tr><td style="font-size:15px;color:#334155;padding-bottom:24px;">` + html.EscapeString(body) + `</td></tr>
          <tr><td style="padding-bottom:24px;">
            <a href="` + html.EscapeString(link) + `" style="display:inline-block;background:#0f766e;color:#ffffff;text-decoration:none;font-weight:600;padding:12px 24px;border-radius:8px;">` + html.EscapeString(cta) + `</a>
          </td></tr>
          <tr><td style="font-size:13px;color:#94a3b8;word-break:break-all;padding-bottom:16px;">` + html.EscapeString(link) + `</td></tr>
          <tr><td style="font-size:13px;color:#94a3b8;border-top:1px solid #e2e8f0;padding-top:16px;">` + html.EscapeString(footer) + `<br/>Sent by <a href="` + html.EscapeString(baseURL) + `" style="color:#0f766e;">Sendwise</a></td></tr>
        </table>
      </td></tr>
    </table>
  </body>
</html>`
}
