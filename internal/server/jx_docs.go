package server

import "bytes"

// DocsMarkdown returns the embedded user manual (docs.md), for `exe docs`.
func DocsMarkdown() []byte { return bytes.Clone(docsMD) }

// SkillMarkdown returns the embedded agent skill guide (skill.md), for
// `exe skill`.
func SkillMarkdown() []byte { return bytes.Clone(skillMD) }
