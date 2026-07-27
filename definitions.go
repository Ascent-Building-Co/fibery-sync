package fibery

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// InitConfig holds the framework-provided values available during integration
// initialization. It is constructed from the resolved server options and passed
// once to Init before the server begins serving requests.
//
// HTTPClient is http.DefaultClient if not explicitly set via WithHTTPClient.
// Logger is slog.Default() and reflects whatever the caller has set via
// slog.SetDefault.
//
// MaxBody mirrors the value passed to WithMaxBody (zero means no limit).
// Custom routes registered via IntegrationWithRoutes can read this to apply
// the same body-size policy the framework uses for built-in routes.
type InitConfig struct {
	Env        EnvProvider
	HTTPClient *http.Client
	Logger     *slog.Logger
	MaxBody    int64
}

// Config defines the basic structure of the integration app.
type Config struct {
	ID             string           `json:"id"`
	Name           string           `json:"name"`
	Website        string           `json:"website,omitempty"`
	Version        string           `json:"version"`
	Description    string           `json:"description"`
	Authentication []Authentication `json:"authentication"`
	Sources        []any            `json:"sources"`
	ResponsibleFor Responsibilities `json:"responsibleFor"`
	Actions        []Action         `json:"actions"`
}

// Authentication defines the different authentication methods that are possible for the app. The Description and Fields values are optional.
type Authentication struct {
	ID             AuthType          `json:"id"`
	Name           string            `json:"name"`
	Description    string            `json:"description,omitempty"`
	Fields         []AuthField       `json:"fields,omitempty"`
	ResponsibleFor *Responsibilities `json:"responsibleFor,omitempty"`
}

// AuthType defines the type of authentication for an integration.
type AuthType string

const (
	AuthNone                  AuthType = "none"
	AuthToken                 AuthType = "token"
	AuthOAuth                 AuthType = "oauth"
	AuthOAuth2                AuthType = "oauth2"
	AuthOAuth2ExternalActions AuthType = "oauth2-external-actions"
)

// AuthField defines authentication specific fields like username, token, ect.
type AuthField struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Type        ConfigFieldType `json:"type"`
	Description string          `json:"description"`
	Optional    bool            `json:"optional,omitempty"`
	// DefaultValue sets the default value for the field when the authentication is presented to the user.
	DefaultValue string `json:"value,omitempty"`
}

// FieldType defines app schema fields. Not all fields are valid for all structs.
type ConfigFieldType string

const (
	// ConfigTextField - Value: UTF-8 string.
	ConfigTextField ConfigFieldType = "text"
	// ConfigNumberField - Value: int or float.
	ConfigNumberField ConfigFieldType = "number"
	// ConfigBoolField - Value: bool.
	ConfigBoolField ConfigFieldType = "bool"
	// ConfigPasswordField - Value: string, used only in account schemas during authentication, rendered as a password input.
	ConfigPasswordField ConfigFieldType = "password"
	// ConfigLinkField - Value: none, LinkField is used to display a URL in authentication view. DefaultValue should specify the link URL.
	ConfigLinkField ConfigFieldType = "link"
	// ConfigHighlightTextField - Value: string, HighlightTextField provides the user a text field with syntax highlighting. The syntax is specified by the EditorMode field.
	ConfigHighlightTextField ConfigFieldType = "highlightText"
	// ConfigDateboxField - Value: string, can be parsed into a time.Time value. DateboxField provides a single date selector for the user.
	ConfigDateboxField ConfigFieldType = "datebox"
	// ConfigDateField - Value: string, can be parsed into a time.Time value. DateField provides the user a DSL date input field. Review the fibery API docs for more details about the Date grammar.
	ConfigDateField ConfigFieldType = "date"
	// ConfigOauthField - Value: none, OauthField sets the authentication view for Oauth or Oauth2 authentication.
	ConfigOauthField ConfigFieldType = "oauth"
	// ConfigListField - Value: any, ListField provides a single-select list for the user to select from. Optional "datalist_requires" can be specified. For example "datalist_requires" = ["owner"] means that fetching the list depends on "owner" field.
	ConfigListField ConfigFieldType = "list"
	// ConfigMultiDropdownField - Value: []any, MultiDropdownField provides a multi-select list for the user to select from. Optional "datalist_requires" can be specified. For example "datalist_requires" = ["owner"] means that fetching the list depends on "owner" field.
	ConfigMultiDropdownField ConfigFieldType = "multidropdown"
)

type EditorMode string

const (
	JSONMode EditorMode = "json"
	SQLMode  EditorMode = "sql"
)

// Responsibilities define what services the app provides.
type Responsibilities struct {
	Sync          bool  `json:"dataSynchronization,omitempty"`
	Import        bool  `json:"dataImport,omitempty"`
	Automations   bool  `json:"automations,omitempty"`
	DataProviding *bool `json:"dataProviding,omitempty"`
	UserAuth      *bool `json:"userAuthentication,omitempty"`
}

// Action defines an automation action to modify third party systems
type Action struct {
	ID          string      `json:"action"`
	Name        string      `json:"name"`
	Icon        string      `json:"icon,omitempty"`
	Description string      `json:"description,omitempty"`
	Args        []ActionArg `json:"args"`
}

type ActionArg struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	Description      string             `json:"description,omitempty"`
	ArgType          ActionArgumentType `json:"type"`
	TextTemplate     bool               `json:"textTemplateSupported,omitempty"`
	Required         bool               `json:"required,omitempty"`
	DefaultValue     any                `json:"defaultValue,omitempty"`
	Options          []map[string]any   `json:"options,omitempty"`
	SendAsFiberyLink bool               `json:"sendAsLinkOfFiberyFiles,omitempty"`
}

type ActionArgumentType string

const (
	TextArg         ActionArgumentType = "text"
	TextAreaArg     ActionArgumentType = "textarea"
	CheckboxArg     ActionArgumentType = "checkbox"
	SingleSelectArg ActionArgumentType = "single-select"
	FilesArg        ActionArgumentType = "files"
)

type ActionRequest[A any] struct {
	Action struct {
		Action string          `json:"action"`
		Args   json.RawMessage `json:"args"`
	} `json:"action"`
	Account Account[A] `json:"account"`
}

type ActionResponse struct {
	Data any `json:"data,omitempty"`
}

// SyncConfig specifies available data types to be synced from the integration source, data syncronization filters,and webhook config if available.
type SyncConfig struct {
	Types    []SyncConfigType `json:"types"`
	Filters  []FilterDef      `json:"filters"`
	Webhooks struct {
		Enabled bool              `json:"enabled"`
		CfgType WebhookConfigType `json:"type"`
	} `json:"webhooks"`
}

type SyncConfigType struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type WebhookConfigType string

const WebhookUIConfig WebhookConfigType = "ui"

type FilterDef struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	Type  FilterType `json:"type"`
	// Datalist should be set to True when Filter.Type is ListFilter or MultiDropdownFilter.
	Datalist bool `json:"datalist,omitempty"`
	Optional bool `json:"optional,omitempty"`
	// Secured set whether a user other than the integration owner can modify this filter.
	Secured          bool     `json:"secured,omitempty"`
	DefaultValue     any      `json:"defaultValue,omitempty"`
	DatalistRequires []string `json:"datalist_requires,omitempty"`
}

// FilterType defines implemented filter types that appear in the fibery UI for filtering data synchronization. There may be some addition filter types not listed in the fibery API docs.
type FilterType string

const (
	// TextFilter provides a text field.
	TextFilter FilterType = "text"
	// NumberFilter provices a number field.
	NumberFilter FilterType = "number"
	// DateboxFilter provides a datebox for a single date.
	DateboxFilter FilterType = "datebox"
	// BoolFilter provides a checkbox.
	BoolFilter FilterType = "bool"
	// ListFilter provides a single-select. When this FilterType is selected, Filter.Datalist should be set to True. The list values are provided by the POST /api/v1/synchronizer/datalist endpoint. List values can be filtered using previously selected list values. Specify the list fields that this list is filtered by in the Filter.DatalistRequires field.
	ListFilter FilterType = "list"
	// MultiDropdownFilter provides a multi-select. When this FilterType is selected, Filter.Datalist should be set to True. The list values are provided by the POST /api/v1/synchronizer/datalist endpoint. List values can be filtered using previously selected list values. Specify the list fields that this list is filtered by in the Filter.DatalistRequires field.
	MultiDropdownFilter FilterType = "multidropdown"
)

type DataField struct {
	Name         string             `json:"name"`
	Ignore       bool               `json:"ignore,omitzero"`
	Description  string             `json:"description,omitzero"`
	ReadOnly     bool               `json:"readonly,omitzero"`
	FieldType    DataFieldType      `json:"type,omitzero"`
	FieldSubType DataFieldSubtype   `json:"subType,omitzero"`
	Format       *DataFieldFormat   `json:"format,omitempty"`
	Options      []DataFieldOptions `json:"options,omitempty"`
	Relation     *Relation          `json:"relation,omitempty"`
}

type DataFieldType string

const (
	ID        DataFieldType = "id"
	Text      DataFieldType = "text"
	Number    DataFieldType = "number"
	DateType  DataFieldType = "date"
	TextArray DataFieldType = "array[text]"
)

type DataFieldSubtype string

const (
	URL          DataFieldSubtype = "url"
	Integer      DataFieldSubtype = "integer"
	Email        DataFieldSubtype = "email"
	Boolean      DataFieldSubtype = "boolean"
	HTML         DataFieldSubtype = "html"
	MD           DataFieldSubtype = "md"
	File         DataFieldSubtype = "file"
	Daterange    DataFieldSubtype = "date-range"
	Title        DataFieldSubtype = "title"
	SingleSelect DataFieldSubtype = "single-select"
	MultiSelect  DataFieldSubtype = "multi-select"
	Day          DataFieldSubtype = "day"
	Icon         DataFieldSubtype = "icon"
	Workflow     DataFieldSubtype = "workflow"
	Avatar       DataFieldSubtype = "avatar"
	Location     DataFieldSubtype = "location"
)

type DataFieldFormat struct {
	Format            DataFieldFormatType `json:"format"`
	CurrencyCode      string              `json:"currencyCode,omitempty"`
	Unit              string              `json:"unit,omitempty"`
	ThousandSeparator bool                `json:"hasThousandSeparator,omitempty"`
	Precision         int                 `json:"precision,omitempty"`
}

type DataFieldFormatType string

const (
	MoneyFormat   DataFieldFormatType = "Money"
	PercentFormat DataFieldFormatType = "Percent"
	NumberFormat  DataFieldFormatType = "Number"
	PhoneFormat   DataFieldFormatType = "phone"
)

type DataFieldOptions struct {
	Name    string              `json:"name"`
	Icon    string              `json:"icon,omitempty"`
	Color   string              `json:"color,omitempty"`
	Default bool                `json:"default,omitempty"`
	Final   bool                `json:"final,omitempty"`
	Type    DataFieldOptionType `json:"type,omitempty"`
}

type DataFieldOptionType string

const (
	NotStarted DataFieldOptionType = "Not started"
	Started    DataFieldOptionType = "Started"
	Finished   DataFieldOptionType = "Finished"
)

type Relation struct {
	Cardinality   CardinalityType `json:"cardinality"`
	Name          string          `json:"name"`
	TargetName    string          `json:"targetName"`
	TargetType    string          `json:"targetType"`
	TargetFieldID string          `json:"targetFieldId"`
	Kind          RelationKind    `json:"kind,omitempty"`
}

type CardinalityType string

const (
	OTO CardinalityType = "one-to-one"
	MTO CardinalityType = "many-to-one"
	MTM CardinalityType = "many-to-many"
)

type RelationKind string

const NativeRelation RelationKind = "native"

type DataType interface {
	ID() string
	Name() string
	Schema() map[string]DataField
}

type Account[A any] struct {
	ID              string    `json:"_id,omitempty"`
	App             string    `json:"app"`
	Owner           string    `json:"owner"`
	MasterAccountID string    `json:"masterAccountId,omitempty"`
	LastUpdatedOn   time.Time `json:"lastUpdatedOn"`
	Enabled         bool      `json:"enabled"`
	Auth            string    `json:"auth"`

	AccountFields A `json:"-"`

	// gate holds the framework-injected account-gate secret submitted by the
	// user (see WithAccountGate). It is extracted from the reserved wire key
	// during decoding and is never part of AccountFields.
	gate string
}

func (a *Account[A]) UnmarshalJSON(b []byte) error {
	type base struct {
		ID              string    `json:"_id,omitempty"`
		App             string    `json:"app"`
		Owner           string    `json:"owner"`
		MasterAccountID string    `json:"masterAccountId,omitempty"`
		LastUpdatedOn   time.Time `json:"lastUpdatedOn"`
		Enabled         bool      `json:"enabled"`
		Auth            string    `json:"auth"`
		Gate            string    `json:"_integration_gate"`
	}

	var bb base
	if err := json.Unmarshal(b, &bb); err != nil {
		return err
	}

	var af A
	if err := json.Unmarshal(b, &af); err != nil {
		return err
	}

	a.ID = bb.ID
	a.App = bb.App
	a.Owner = bb.Owner
	a.MasterAccountID = bb.MasterAccountID
	a.LastUpdatedOn = bb.LastUpdatedOn
	a.Enabled = bb.Enabled
	a.Auth = bb.Auth
	a.AccountFields = af
	a.gate = bb.Gate
	return nil
}

type ValidateRequest[A any] struct {
	ID     string     `json:"id"`
	Fields Account[A] `json:"fields"`
}

type SyncMetaRequest[A any] struct {
	Types     []string       `json:"types"`
	Account   Account[A]     `json:"account"`
	Filter    map[string]any `json:"filter"`
	CreatedAt time.Time      `json:"createdAt"`
	Workspace any            `json:"workspace,omitempty"`
	Space     string         `json:"space,omitempty"`
	UserID    string         `json:"userId,omitempty"`
}

type SyncSchema map[string]map[string]DataField

type DataRequest[A, P any] struct {
	RequestedType     string                          `json:"requestedType"`
	OperationID       string                          `json:"operationId"`
	Types             []string                        `json:"types"`
	Schema            map[string]map[string]DataField `json:"schema"`
	Filter            map[string]any                  `json:"filter"`
	Account           Account[A]                      `json:"account"`
	LastSyncronizedAt time.Time                       `json:"lastSynchronizedAt"`
	CreatedAt         time.Time                       `json:"createdAt"`
	Workspace         any                             `json:"workspace,omitempty"`
	Pagination        P                               `json:"pagination,omitempty"`
}

type DataResponse[P any] struct {
	Items               []map[string]any `json:"items"`
	Pagination          Pagination[P]    `json:"pagination"`
	SynchronizationType SyncType         `json:"synchronizationType,omitempty"`
}

type Pagination[P any] struct {
	HasNext        bool `json:"hasNext"`
	NextPageConfig P    `json:"nextPageConfig,omitempty"`
}

type SyncType string

const (
	Delta SyncType = "delta"
	Full  SyncType = "full"
)

type SyncAction string

const (
	SET    SyncAction = "SET"
	REMOVE SyncAction = "REMOVE"
)

type DatalistRequest[A any] struct {
	Types     []string          `json:"types"`
	Account   Account[A]        `json:"account"`
	Field     string            `json:"field"`
	DependsOn map[string]string `json:"dependsOn"`
}

type DatalistResponse struct {
	Items []struct {
		Title string `json:"title"`
		Value any    `json:"value"`
	} `json:"items"`
}

type FilterValidateRequest[A any] struct {
	Types   []string       `json:"types"`
	Account Account[A]     `json:"account"`
	Filter  map[string]any `json:"filter"`
}

type ResourceRequest[A, P any] struct {
	Types   []string       `json:"types"`
	Account Account[A]     `json:"account"`
	Filter  map[string]any `json:"filter"`
	Params  P              `json:"params"`
}

type ResourceStream struct {
	Body        io.ReadCloser // required on success
	ContentType string        // e.g. "application/pdf", "image/png", "application/octet-stream"
	Length      *int64        // optional; nil if unknown
	Filename    string        // optional; for Content-Disposition
}

type Oauth1AuthRequest struct {
	CallbackURI string `json:"callback_uri"`
}

type Oauth1AuthResponse struct {
	RedirectURI string `json:"redirect_uri"`
	Token       string `json:"token"`
	Secret      string `json:"secret"`
}

// Oauth1TokenRequest is the request body for POST /oauth1/v1/access_token.
// The fixed fields (Fields, OauthVerifier) are decoded from the known JSON
// structure. Extra is populated from the same top-level JSON object and
// captures any additional parameters appended by the third-party provider
// alongside OauthVerifier upon redirect. Use struct{} if no extra fields are
// expected.
type Oauth1TokenRequest[F any] struct {
	Fields struct {
		AccessToken  string `json:"access_token"`
		AccessSecret string `json:"access_secret"`
		CallbackURI  string `json:"callback_uri"`
	} `json:"fields"`
	OauthVerifier string `json:"oauth_verifier"`
	Extra         F
}

func (r *Oauth1TokenRequest[F]) UnmarshalJSON(b []byte) error {
	type base struct {
		Fields struct {
			AccessToken  string `json:"access_token"`
			AccessSecret string `json:"access_secret"`
			CallbackURI  string `json:"callback_uri"`
		} `json:"fields"`
		OauthVerifier string `json:"oauth_verifier"`
	}

	var bb base
	if err := json.Unmarshal(b, &bb); err != nil {
		return err
	}

	var extra F
	if err := json.Unmarshal(b, &extra); err != nil {
		return err
	}

	r.Fields = bb.Fields
	r.OauthVerifier = bb.OauthVerifier
	r.Extra = extra
	return nil
}

const OAuth2DefaultCallbackURI = "https://oauth-svc.fibery.io/callback"

type Oauth2AuthRequest struct {
	CallbackURI string         `json:"callback_uri"`
	State       string         `json:"state"`
	Fields      map[string]any `json:"fields,omitempty"`
	Auth        string         `json:"auth"`
}

type Oauth2AuthResponse struct {
	RedirectURI string `json:"redirect_uri"`
}

// Oauth2TokenRequest is the request body for POST /oauth2/v1/access_token.
// The fixed fields (Fields, Code) are decoded from the known JSON structure.
// Extra is populated from the same top-level JSON object and captures any
// additional parameters appended by the third-party provider alongside Code
// upon redirect (e.g. realmId for QuickBooks). Use struct{} if no extra fields
// are expected.
type Oauth2TokenRequest[F any] struct {
	Service  string `json:"service"`
	Host     string `json:"host"`
	Hostname string `json:"hostname"`
	Fields   struct {
		ID          string    `json:"_id,omitempty"`
		App         string    `json:"app"`
		CallbackURI string    `json:"callback_uri"`
		Auth        string    `json:"auth"`
		Owner       string    `json:"owner"`
		State       string    `json:"state"`
		ExpireOn    time.Time `json:"expireOn"`
	} `json:"fields"`
	Code  string `json:"code"`
	Extra F
}

func (r *Oauth2TokenRequest[F]) UnmarshalJSON(b []byte) error {
	type base struct {
		Service  string `json:"service"`
		Host     string `json:"host"`
		Hostname string `json:"hostname"`
		Fields   struct {
			ID          string    `json:"_id,omitempty"`
			App         string    `json:"app"`
			CallbackURI string    `json:"callback_uri"`
			Auth        string    `json:"auth"`
			Owner       string    `json:"owner"`
			State       string    `json:"state"`
			ExpireOn    time.Time `json:"expireOn"`
		} `json:"fields"`
		Code string `json:"code"`
	}

	var bb base
	if err := json.Unmarshal(b, &bb); err != nil {
		return err
	}

	var extra F
	if err := json.Unmarshal(b, &extra); err != nil {
		return err
	}

	r.Fields = bb.Fields
	r.Code = bb.Code
	r.Extra = extra
	return nil
}

type WebhookInitRequest[A, P any] struct {
	Types     []string         `json:"types"`
	Account   Account[A]       `json:"account"`
	Filter    map[string]any   `json:"filter"`
	Workspace any              `json:"workspace,omitempty"`
	Space     string           `json:"space,omitempty"`
	UserID    string           `json:"userId,omitempty"`
	Webhook   WebhookConfig[P] `json:"webhook"`
}

type WebhookConfig[P any] struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	InitParams  P      `json:"params,omitempty"`
}

type WebhookPreProcessResponse[R any] struct {
	Reply        R        `json:"reply"`
	WorkspaceIDs []string `json:"workspaceIds"`
}

type WebhookTransformRequest[PR, PL, A any] struct {
	Params  PR             `json:"params"`
	Payload PL             `json:"payload"`
	Types   []string       `json:"types"`
	Account Account[A]     `json:"account"`
	Filter  map[string]any `json:"filter"`
}

type WebhookTransformResponse struct {
	Data WebhookData `json:"data"`
}

type WebhookData map[string][]map[string]any

// LogoResponse is returned from IntegrationWithLogo.Logo. Exactly one of
// Body or URL should be set:
//
//   - Body non-nil:           framework streams it as image/svg+xml with 200.
//   - URL non-nil:            framework returns a 302 redirect to URL.
//   - Both nil (zero value):  framework returns 204 No Content (Fibery
//     falls back to its default app logo).
//
// Returning an *IntegrationError from Logo is treated as "no logo": the
// framework logs the error and responds with 204 so Fibery can substitute
// its default logo, per the Fibery custom-app spec.
type LogoResponse struct {
	Body io.ReadCloser
	URL  *url.URL
}
