package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Announcement holds the schema definition for the Announcement entity.
type Announcement struct {
	ent.Schema
}

// Annotations of the Announcement.
func (Announcement) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "announcements"},
	}
}

// Mixin of the Announcement.
func (Announcement) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

// Fields of the Announcement.
func (Announcement) Fields() []ent.Field {
	return []ent.Field{
		field.String("title").
			MaxLen(200).
			NotEmpty().
			Comment("公告标题"),
		field.String("content").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			MaxLen(10000).
			NotEmpty().
			Comment("公告内容"),
		field.Bool("enabled").
			Default(false).
			Comment("是否启用"),
		field.Int("priority").
			Default(0).
			Min(0).
			Max(100).
			Comment("优先级，数字越大越靠前"),
	}
}

// Edges of the Announcement.
func (Announcement) Edges() []ent.Edge {
	return nil
}

// Indexes of the Announcement.
func (Announcement) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("enabled"),
		index.Fields("priority"),
		index.Fields("created_at"),
	}
}
