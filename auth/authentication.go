package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gotify/server/model"
)

const (
	headerName = "X-Gotify-Key"
)

// The Database interface for encapsulating database access.
type Database interface {
	GetApplicationByToken(token string) (*model.Application, error)
	GetClientByToken(token string) (*model.Client, error)
	GetPluginConfByToken(token string) (*model.PluginConf, error)

	GetUserByName(name string) (*model.User, error)
	GetUserByID(id uint) (*model.User, error)
	CreateUser(user *model.User) error
}

// AuthenticationProvider provides authentication methods
type AuthenticationProvider interface {
	Name() string
	Authenticate(req *http.Request) (user *model.User, err error)
}

// Auth is the provider for authentication middleware
type Auth struct {
	authenticators []AuthenticationProvider
	DB             Database
}

// RegisterAuthenticationProvider registers a new authentication provider
// the provider is used in the order it i registered
func (a *Auth) RegisterAuthenticationProvider(auth AuthenticationProvider) {
	a.authenticators = append(a.authenticators, auth)
}

func (a *Auth) getUserInfoFromAuth(ctx *gin.Context) (user *model.User, authenticator string, err error) {
	designatedAuthenticator := ctx.GetHeader("X-Gotify-Authenticator")

	for _, authProvider := range a.authenticators {
		providerName := authProvider.Name()
		if designatedAuthenticator != "" && providerName != designatedAuthenticator {
			continue
		}
		if user, err := authProvider.Authenticate(ctx.Request); user != nil {
			return user, providerName, nil
		} else if err != nil {
			return nil, providerName, err
		}
	}
	return nil, "", NoAuthProviderError{}
}

// ObtainAuthentication attempts to get user authentication from either client token or an authentication provider
func (a *Auth) ObtainAuthentication(ctx *gin.Context) (user *model.User, err error) {
	// 1. check for existing client token
	if token := tokenFromQueryOrHeader(ctx); token != "" {
		client, _ := a.DB.GetClientByToken(token)
		if client == nil {
			return nil, TokenRequiredError{"client"}
		}
		user, _ := a.DB.GetUserByID(client.UserID)
		if user == nil {
			return nil, TokenRequiredError{"client"}
		}
		return user, nil
	}

	// 2. try user authentication
	user, authenticator, err := a.getUserInfoFromAuth(ctx)
	if err != nil {
		return nil, err
	}
	if user.ID == 0 {
		newUser := &model.User{
			Name:          user.Name,
			Authenticator: authenticator,
			Admin:         user.Admin,
		}
		if err := a.DB.CreateUser(newUser); err != nil {
			return nil, err
		}
		user = newUser
	}
	return a.DB.GetUserByName(user.Name)
}

// RequireAdmin returns a gin middleware which requires a client token or authentication to be supplied
// with the request. Also the authenticated user must be an administrator.
func (a *Auth) RequireAdmin() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		user, err := a.ObtainAuthentication(ctx)
		if err != nil {
			abortContextWithAuthenticationError(ctx, err)
			return
		}
		if !user.Admin {
			abortContextWithAuthenticationError(ctx, NotAdminError{})
			return
		}
		RegisterAuthentication(ctx, user, tokenFromQueryOrHeader(ctx))
	}
}

// RequireClient returns a gin middleware which requires a client token or basic authentication header to be supplied
// with the request.
func (a *Auth) RequireClient() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		user, err := a.ObtainAuthentication(ctx)
		if err != nil {
			abortContextWithAuthenticationError(ctx, err)
			return
		}
		RegisterAuthentication(ctx, user, tokenFromQueryOrHeader(ctx))
	}
}

// RequireApplicationToken returns a gin middleware which requires an application token to be supplied with the request.
func (a *Auth) RequireApplicationToken() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		token := tokenFromQueryOrHeader(ctx)
		app, _ := a.DB.GetApplicationByToken(token)
		if app == nil {
			abortContextWithAuthenticationError(ctx, TokenRequiredError{"application"})
			return
		}
		user, err := a.DB.GetUserByID(app.UserID)
		if err != nil {
			abortContextWithAuthenticationError(ctx, TokenRequiredError{"application"})
			return
		}
		RegisterAuthentication(ctx, user, token)
	}
}

func tokenFromQueryOrHeader(ctx *gin.Context) string {
	if token := tokenFromQuery(ctx); token != "" {
		return token
	} else if token := tokenFromHeader(ctx); token != "" {
		return token
	}
	return ""
}

func tokenFromQuery(ctx *gin.Context) string {
	return ctx.Request.URL.Query().Get("token")
}

func tokenFromHeader(ctx *gin.Context) string {
	return ctx.Request.Header.Get(headerName)
}
