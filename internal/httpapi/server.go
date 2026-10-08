package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Felix-cy110/house-rent-table/internal/analysis"
	"github.com/Felix-cy110/house-rent-table/internal/document"
	"github.com/Felix-cy110/house-rent-table/internal/importer"
)

type Options struct {
	Analyzer     analysis.Analyzer
	WebDir       string
	TemplatePath string
}

func New(opts Options) http.Handler {
	if opts.Analyzer == nil {
		opts.Analyzer = analysis.Unconfigured{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/import", importExcel)
	mux.HandleFunc("POST /api/analyze", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 24<<20)
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "请使用 JSON 提交分析内容")
			return
		}
		var doc document.Document
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&doc); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_document", "分析内容格式不正确，请重新上传表格")
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			writeError(w, http.StatusBadRequest, "invalid_document", "请求只能包含一份表格数据")
			return
		}
		if err := doc.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_document", err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		result, err := opts.Analyzer.Analyze(ctx, doc)
		if errors.Is(err, analysis.ErrNotConfigured) {
			writeError(w, http.StatusNotImplemented, "analysis_not_configured", "表格已读取，风险分析暂未开放。当前没有生成任何风险判断。")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadGateway, "analysis_failed", "本次分析未完成，请稍后重试")
			return
		}
		if result.Findings == nil {
			result.Findings = []analysis.Finding{}
		}
		if result.MissingInformation == nil {
			result.MissingInformation = []analysis.MissingInformation{}
		}
		for i := range result.Findings {
			if result.Findings[i].EvidenceFieldIDs == nil {
				result.Findings[i].EvidenceFieldIDs = []string{}
			}
		}
		writeJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("GET /api/template", func(w http.ResponseWriter, r *http.Request) {
		if opts.TemplatePath == "" {
			writeError(w, http.StatusNotFound, "template_unavailable", "空白模板暂时不可下载")
			return
		}
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "租房信息模板.xlsx"}))
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		http.ServeFile(w, r, opts.TemplatePath)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "接口不存在")
	})
	files := http.FileServer(http.Dir(opts.WebDir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "不支持的请求方法", http.StatusMethodNotAllowed)
			return
		}
		if _, err := os.Stat(filepath.Join(opts.WebDir, "index.html")); err != nil {
			http.Error(w, "页面暂未准备好", http.StatusServiceUnavailable)
			return
		}
		files.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		mux.ServeHTTP(w, r)
	})
}

func importExcel(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, importer.MaxFileBytes+(1<<20))
	if err := r.ParseMultipartForm(importer.MaxFileBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "file_too_large", "文件不能超过 10 MB")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_upload", "请上传一份 .xlsx 文件")
		}
		return
	}
	defer r.MultipartForm.RemoveAll()
	count := 0
	for _, uploads := range r.MultipartForm.File {
		count += len(uploads)
	}
	files := r.MultipartForm.File["file"]
	if count != 1 || len(files) != 1 {
		writeError(w, http.StatusBadRequest, "invalid_upload", "每次只能上传一份 Excel，请使用 file 字段")
		return
	}
	header := files[0]
	if header.Size > importer.MaxFileBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "file_too_large", "文件不能超过 10 MB")
		return
	}
	if !strings.EqualFold(filepath.Ext(header.Filename), ".xlsx") {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_file", "目前支持 .xlsx 文件，请在 Excel 中另存为此格式")
		return
	}
	file, err := header.Open()
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload", "文件读取失败，请重新上传")
		return
	}
	defer file.Close()
	result, err := importer.Parse(r.Context(), file, header.Filename)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_workbook", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
