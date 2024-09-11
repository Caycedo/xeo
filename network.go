package xeo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

func (a *API) doGet(reqURL string) ([]byte, error) {
	resp, err := a.client.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (a *API) doPost(reqURL string, files ...content) ([]byte, error) {
	var (
		buf = new(bytes.Buffer)
		w   = multipart.NewWriter(buf)
	)

	for _, f := range files {
		part, err := w.CreateFormFile(f.ftype, filepath.Base(f.fname))
		if err != nil {
			return nil, err
		}
		part.Write(f.fdata)
	}
	w.Close()

	req, err := http.NewRequest(http.MethodPost, reqURL, buf)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Content-Type", w.FormDataContentType())

	res, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return io.ReadAll(res.Body)
}

func (a *API) doPostForm(reqURL string, keyVals map[string]string) ([]byte, error) {
	var form = make(url.Values)

	for k, v := range keyVals {
		form.Add(k, v)
	}

	req, err := http.NewRequest(http.MethodPost, reqURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.PostForm = form
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	res, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	return io.ReadAll(res.Body)
}

func (a *API) sendFile(file, thumbnail InputFile, url, fileType string) (res []byte, err error) {
	var cnt []content

	if file.id != "" {
		url = fmt.Sprintf("%s&%s=%s", url, fileType, file.id)
	} else if file.url != "" {
		url = fmt.Sprintf("%s&%s=%s", url, fileType, file.url)
	} else if c, e := toContent(fileType, file); e == nil {
		cnt = append(cnt, c)
	} else {
		err = e
	}

	if c, e := toContent("thumbnail", thumbnail); e == nil {
		cnt = append(cnt, c)
	} else {
		err = e
	}

	if len(cnt) > 0 {
		res, err = a.doPost(url, cnt...)
	} else {
		res, err = a.doGet(url)
	}
	return
}

func (a *API) get(endpoint string, vals url.Values, v APIResponse) error {
	furl, err := url.JoinPath(a.baseURL, endpoint)
	if err != nil {
		return err
	}

	if vals != nil {
		if queries := vals.Encode(); queries != "" {
			furl = fmt.Sprintf("%s?%s", furl, queries)
		}
	}

	cnt, err := a.doGet(furl)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(cnt, v); err != nil {
		return err
	}
	return check(v)
}

func (a *API) postFile(endpoint, fileType string, file, thumbnail InputFile, vals url.Values, v APIResponse) error {
	furl, err := joinURL(a.baseURL, endpoint, vals)
	if err != nil {
		return err
	}

	cnt, err := a.sendFile(file, thumbnail, furl, fileType)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(cnt, v); err != nil {
		return err
	}
	return check(v)
}

func (a *API) postMedia(endpoint string, editSingle bool, vals url.Values, v APIResponse, files ...InputMedia) error {
	furl, err := joinURL(a.baseURL, endpoint, vals)
	if err != nil {
		return err
	}

	cnt, err := a.sendMediaFiles(furl, editSingle, files...)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(cnt, v); err != nil {
		return err
	}
	return check(v)
}

func (a *API) postStickers(endpoint string, vals url.Values, v APIResponse, stickers ...InputSticker) error {
	furl, err := joinURL(a.baseURL, endpoint, vals)
	if err != nil {
		return err
	}

	cnt, err := a.sendStickers(furl, stickers...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(cnt, v); err != nil {
		return err
	}
	return check(v)
}

func (a *API) sendMediaFiles(url string, editSingle bool, files ...InputMedia) (res []byte, err error) {
	var (
		med []mediaEnvelope
		cnt []content
		jsn []byte
	)

	for _, file := range files {
		var im mediaEnvelope
		var cntArr []content

		media := file.media()
		thumbnail := file.thumbnail()

		im, cntArr, err = processMedia(media, thumbnail)
		if err != nil {
			return
		}

		im.InputMedia = file

		med = append(med, im)
		cnt = append(cnt, cntArr...)
	}

	if editSingle {
		jsn, err = json.Marshal(med[0])
	} else {
		jsn, err = json.Marshal(med)
	}

	if err != nil {
		return
	}

	url = fmt.Sprintf("%s&media=%s", url, jsn)

	if len(cnt) > 0 {
		return a.doPost(url, cnt...)
	}
	return a.doGet(url)
}

func (a *API) sendStickers(url string, stickers ...InputSticker) (res []byte, err error) {
	var (
		sti []stickerEnvelope
		cnt []content
		jsn []byte
	)

	for _, s := range stickers {
		var se stickerEnvelope
		var cntArr []content

		se, cntArr, err = processSticker(s.Sticker)
		if err != nil {
			return
		}

		se.InputSticker = s

		sti = append(sti, se)
		cnt = append(cnt, cntArr...)
	}

	if len(sti) == 1 {
		jsn, _ = json.Marshal(sti[0])
		url = fmt.Sprintf("%s&sticker=%s", url, jsn)
	} else {
		jsn, _ = json.Marshal(sti)
		url = fmt.Sprintf("%s&stickers=%s", url, jsn)
	}

	if len(cnt) > 0 {
		return a.doPost(url, cnt...)
	}
	return a.doGet(url)
}
