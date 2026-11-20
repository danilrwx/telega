package telega

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/auth/qrlogin"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"rsc.io/qr"
)

type loginUI struct {
	requests <-chan request
	events   chan<- serverEvent
}

func emit(ctx context.Context, events chan<- serverEvent, value serverEvent) error {
	select {
	case events <- value:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l loginUI) prompt(ctx context.Context, label string, masked bool, hint string) (string, error) {
	if err := emit(ctx, l.events, serverEvent{kind: "prompt", text: label, masked: masked, hint: hint}); err != nil {
		return "", err
	}

	select {
	case input := <-l.requests:
		return input.text, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func login(ctx context.Context, c *telegram.Client, ui loginUI, qrMode bool, loggedIn qrlogin.LoggedIn, cfg config) error {
	status, err := c.Auth().Status(ctx)
	if err != nil {
		return err
	}

	if status.Authorized {
		return nil
	}

	if qrMode {
		q := qrlogin.NewQR(c.API(), cfg.APIID, cfg.APIHash, qrlogin.Options{
			Migrate: c.MigrateTo,
			Password: func(ctx context.Context) (string, error) {
				return ui.prompt(ctx, "Two-step password", true, "")
			},
		})
		_, err := q.Auth(ctx, loggedIn, func(ctx context.Context, token qrlogin.Token) error {
			code, err := qr.Encode(token.URL(), qr.M)
			if err != nil {
				return err
			}

			return emit(ctx, ui.events, serverEvent{kind: "qr", text: terminalQR(code)})
		})

		return err
	}

	phone, err := ui.prompt(ctx, "Phone number (+…)", false, "You can also sign in with telega --qr")
	if err != nil {
		return err
	}

	phone = strings.TrimSpace(phone)

	sent, err := c.Auth().SendCode(ctx, phone, auth.SendCodeOptions{})
	if tgerr.Is(err, "SESSION_PASSWORD_NEEDED") {
		return loginPassword(ctx, c.Auth(), ui)
	}

	if err != nil {
		return err
	}

	var delivery *tg.AuthSentCode

	switch response := sent.(type) {
	case *tg.AuthSentCodeSuccess:
		return checkAuthorization(response.Authorization)
	case *tg.AuthSentCode:
		delivery = response
	case *tg.AuthSentCodePaymentRequired:
		return errors.New("login code requires payment. Try telega --qr")
	default:
		return fmt.Errorf("unrecognized response to the Telegram code request: %T", response)
	}

	destination, err := codeDestination(delivery.Type)
	if err != nil {
		return err
	}

	hint := destination
	for range 3 {
		code, err := ui.prompt(ctx, "Telegram code", true, hint)
		if err != nil {
			return err
		}

		req := &tg.AuthSignInRequest{PhoneNumber: phone, PhoneCodeHash: delivery.PhoneCodeHash}
		if _, ok := delivery.Type.(*tg.AuthSentCodeTypeEmailCode); ok {
			req.SetEmailVerification(&tg.EmailVerificationCode{Code: strings.TrimSpace(code)})
		} else {
			req.SetPhoneCode(strings.TrimSpace(code))
		}

		result, err := c.API().AuthSignIn(ctx, req)
		switch {
		case err == nil:
			return checkAuthorization(result)
		case tgerr.Is(err, "PHONE_CODE_INVALID", "EMAIL_CODE_INVALID"):
			hint = "Incorrect code. " + destination
		case tgerr.Is(err, "SESSION_PASSWORD_NEEDED"):
			return loginPassword(ctx, c.Auth(), ui)
		default:
			return err
		}
	}

	return errors.New("login code rejected")
}

func terminalQR(code *qr.Code) string {
	const padding = 4

	var out strings.Builder

	for y := -padding; y < code.Size+padding; y += 2 {
		for x := -padding; x < code.Size+padding; x++ {
			top := code.Black(x, y)

			bottom := code.Black(x, y+1)
			switch {
			case top && bottom:
				out.WriteRune('█')
			case top:
				out.WriteRune('▀')
			case bottom:
				out.WriteRune('▄')
			default:
				out.WriteByte(' ')
			}
		}

		out.WriteByte('\n')
	}

	return out.String()
}

func codeDestination(kind tg.AuthSentCodeTypeClass) (string, error) {
	switch kind := kind.(type) {
	case *tg.AuthSentCodeTypeApp:
		return "Check the «Telegram» service chat on a device where you are signed in.", nil
	case *tg.AuthSentCodeTypeEmailCode:
		return "Check the login email " + kind.EmailPattern + ".", nil
	case *tg.AuthSentCodeTypeSMS, *tg.AuthSentCodeTypeSMSPhrase, *tg.AuthSentCodeTypeSMSWord:
		return "Check the SMS inbox for the number you entered.", nil
	case *tg.AuthSentCodeTypeCall, *tg.AuthSentCodeTypeFlashCall, *tg.AuthSentCodeTypeMissedCall:
		return "Telegram selected delivery by phone call.", nil
	case *tg.AuthSentCodeTypeFragmentSMS:
		return "View the code on Fragment: " + kind.URL, nil
	case *tg.AuthSentCodeTypeSetUpEmailRequired:
		return "", errors.New("login email setup required. Try telega --qr")
	case *tg.AuthSentCodeTypeFirebaseSMS:
		return "", errors.New("mobile verification required. Try telega --qr")
	default:
		return "", fmt.Errorf("unsupported code delivery method: %T. Try telega --qr", kind)
	}
}

func loginPassword(ctx context.Context, client *auth.Client, ui loginUI) error {
	hint := ""
	for range 3 {
		password, err := ui.prompt(ctx, "Two-step password", true, hint)
		if err != nil {
			return err
		}

		_, err = client.Password(ctx, password)
		if !errors.Is(err, auth.ErrPasswordInvalid) {
			return err
		}

		hint = "Incorrect password."
	}

	return errors.New("two-step password rejected")
}

func checkAuthorization(result tg.AuthAuthorizationClass) error {
	if _, ok := result.(*tg.AuthAuthorization); !ok {
		return errors.New("an existing Telegram account is required")
	}

	return nil
}
