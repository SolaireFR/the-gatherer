package news

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/SolaireFR/gosvelte/gosvelte-core/database"
	"github.com/SolaireFR/gosvelte/gosvelte-core/entity"
	httperror "github.com/SolaireFR/gosvelte/gosvelte-core/error"
	"github.com/SolaireFR/gosvelte/gosvelte-core/server"
)

// Types de Topic
const (
	kindTech    = "tech"    // technologie utilisée : l'IA cherche ses alternatives récentes
	kindExclude = "exclude" // sujet bloqué : écarté de toutes les rubriques
)

// maxTopicLength : longueur maximale d'un libellé (il est recopié dans le prompt)
const maxTopicLength = 100

// Topic : une entrée de la page Configuration, envoyée à l'IA à chaque récupération
type Topic struct {
	database.BaseTable
	ID    int    `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // kindTech | kindExclude
}

func (t Topic) Fields() []database.Field {
	return database.Bind(&t,
		database.On(&t.ID),
		database.On(&t.Label).NotNull(),
		database.On(&t.Kind).NotNull(),
	)
}

type NewTopicDto struct {
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// --- Routes (fonction …Endpoints lue par gosvelte generate) ---
func RegisterTopicEndpoints() {
	server.GET("/topics", ListTopics, adminOnly)
	server.POST("/topics", CreateTopic, adminOnly)
	server.DELETE("/topics/{id}", DeleteTopic, adminOnly)
}

func init() {
	entity.Register("Topic", entity.InitFuncForEntity(RegisterTopicEndpoints, database.EnsureTable[Topic]))
}

type ListTopicsParams struct{}

type CreateTopicParams struct {
	Dto server.Body[NewTopicDto]
}

type DeleteTopicParams struct {
	ID server.IntPath
}

func ListTopics(_ ListTopicsParams) ([]Topic, error) {
	topics, err := database.Select[Topic]()
	if err != nil {
		return nil, fmt.Errorf("lecture de la configuration : %w", err)
	}
	return topics, nil
}

// CreateTopic : libellé sans espaces superflus, requis et borné ; type tech ou exclude ; doublon (même type, casse
// ignorée) refusé
func CreateTopic(p CreateTopicParams) (Topic, error) {
	topic := Topic{Label: strings.Join(strings.Fields(p.Dto.Value.Label), " "), Kind: p.Dto.Value.Kind}
	if topic.Kind != kindTech && topic.Kind != kindExclude {
		return Topic{}, httperror.HTTPException{Status: http.StatusBadRequest, Code: "news.topicKindInvalid", Message: "Kind must be tech or exclude"}
	}
	if topic.Label == "" || utf8.RuneCountInString(topic.Label) > maxTopicLength {
		return Topic{}, httperror.HTTPException{Status: http.StatusBadRequest, Code: "news.topicLabelInvalid",
			Message: fmt.Sprintf("Label required (%d characters maximum)", maxTopicLength), Params: map[string]any{"max": maxTopicLength}}
	}
	existing, err := database.Select[Topic](map[string]any{"kind": topic.Kind})
	if err != nil {
		return Topic{}, fmt.Errorf("lecture de la configuration : %w", err)
	}
	for _, t := range existing {
		if strings.EqualFold(t.Label, topic.Label) {
			return Topic{}, httperror.HTTPException{Status: http.StatusConflict, Code: "news.topicExists",
				Message: "Already in the list", Params: map[string]any{"label": t.Label}}
		}
	}
	if err := database.Insert(&topic); err != nil {
		return Topic{}, fmt.Errorf("enregistrement de « %s » : %w", topic.Label, err)
	}
	return topic, nil
}

func DeleteTopic(p DeleteTopicParams) (Topic, error) {
	found, err := database.Select[Topic](map[string]any{"id": p.ID.Value})
	if err != nil {
		return Topic{}, fmt.Errorf("lecture de l'entrée %d : %w", p.ID.Value, err)
	}
	if len(found) == 0 {
		return Topic{}, httperror.HTTPException{Status: http.StatusNotFound, Code: "news.topicNotFound", Message: "Entry not found"}
	}
	if err := database.Delete[Topic](found[0].ID); err != nil {
		return Topic{}, fmt.Errorf("suppression de l'entrée %d : %w", found[0].ID, err)
	}
	return found[0], nil
}
