// Package inference shares Kronk model installation and loading between commands.
package inference

import (
	"context"
	"fmt"
	"github.com/ardanlabs/kronk/sdk/kronk"
	"github.com/ardanlabs/kronk/sdk/kronk/model"
	"github.com/ardanlabs/kronk/sdk/tools/libs"
	"github.com/ardanlabs/kronk/sdk/tools/models"
	"time"
)

const DefaultEmbedding = "bartowski/granite-embedding-278m-multilingual-GGUF:Q8_0"
const DefaultChat = "mistralai/Ministral-3-8B-Instruct-2512-GGUF:Q4_K_M"

func Install(modelEmbedSource, modelChatSource string) (models.Path, models.Path, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	libs, err := libs.New(
		libs.WithDetect(ctx, kronk.FmtLogger),
		libs.WithValidation(true),
	)
	if err != nil {
		return models.Path{}, models.Path{}, err
	}

	if _, err := libs.Download(ctx, kronk.FmtLogger); err != nil {
		return models.Path{}, models.Path{}, fmt.Errorf("unable to install llama.cpp: %w", err)
	}

	if err := kronk.Init(kronk.WithLibPath(libs.LibsPath())); err != nil {
		return models.Path{}, models.Path{}, fmt.Errorf("unable to init kronk: %w", err)
	}

	mdls, err := models.New()
	if err != nil {
		return models.Path{}, models.Path{}, fmt.Errorf("unable to create models api: %w", err)
	}

	infoEmbed, err := mdls.Download(ctx, kronk.FmtLogger, modelEmbedSource)
	if err != nil {
		return models.Path{}, models.Path{}, fmt.Errorf("unable to install model: %w", err)
	}

	if modelChatSource == "" {
		return infoEmbed, models.Path{}, nil
	}
	infoChat, err := mdls.Download(ctx, kronk.FmtLogger, modelChatSource)
	if err != nil {
		return models.Path{}, models.Path{}, fmt.Errorf("unable to install model: %w", err)
	}

	return infoEmbed, infoChat, nil
}

func New(mp models.Path, extra ...model.Option) (*kronk.Kronk, error) {
	options := []model.Option{model.WithModelFiles(mp.ModelFiles), model.WithAutoTune(true)}
	options = append(options, extra...)
	krn, err := kronk.New(options...)
	if err != nil {
		return nil, fmt.Errorf("unable to create inference model: %w", err)
	}

	fmt.Print("- system info:\n\t")
	for k, v := range krn.SystemInfo() {
		fmt.Printf("%s:%v, ", k, v)
	}
	fmt.Println()

	fmt.Println("- contextWindow:", krn.ModelConfig().ContextWindow())
	fmt.Println("- embeddings   :", krn.ModelInfo().IsEmbedModel)
	fmt.Println("- template     :", krn.ModelInfo().Template.FileName)

	return krn, nil
}
