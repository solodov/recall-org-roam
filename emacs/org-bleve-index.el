;;; org-bleve-index.el --- Keep an external Bleve index up to date for Org files -*- lexical-binding: t; -*-

;; Copyright (C) 2026 Peter Solodov

;; Author: Peter Solodov
;; Keywords: outlines, tools
;; Package-Requires: ((emacs "28.1"))
;; Version: 0.1.0

;;; Commentary:

;; org-bleve-index keeps an external Bleve index maintained by the `org-search`
;; binary in sync with file-backed Org buffers.
;;
;; To integrate it into Emacs:
;;
;; 1. Install the `org-search` binary and make it available on PATH, or set
;;    `org-bleve-index-command` to the absolute binary path.
;; 2. Put your org-search txtpb config in the default XDG location, or set
;;    `org-bleve-index-config-file` to the config file you want Emacs to use.
;; 3. Load this file and enable `org-bleve-index-mode`.
;;
;; Minimal setup:
;;
;;   (add-to-list 'load-path "/path/to/org-search/emacs")
;;   (require 'org-bleve-index)
;;   (org-bleve-index-mode 1)
;;
;; With `org-bleve-index-mode` enabled, file-backed Org buffers trigger
;; asynchronous `org-search update-file --json ...` commands after save. Manual
;; rebuild is available through `org-bleve-index-rebuild`, and the diagnostics
;; buffer can be opened with `org-bleve-index-open-diagnostics`.
;;
;; v1 intentionally focuses on save-time content edits. That covers normal Org
;; edits well, but rename and delete workflows are only partially covered. When
;; you suspect the index may have drifted after file moves or other filesystem
;; lifecycle events, run `org-bleve-index-rebuild` as the repair path.
;;
;; The save-driven contract is still useful in v1: ordinary saves stay
;; incremental, and missing-file cleanup can still happen through the
;; `org-search` CLI when the relevant path is updated there. Future work can
;; improve rename/delete handling through explicit commands, editor hooks for
;; rename-like operations, or broader filesystem observation.
;;
;; The naming boundary is also intentional for now: the Emacs package is named
;; `org-bleve-index`, while the standalone CLI binary remains `org-search`.
;;
;; This package intentionally keeps a narrow scope: it maintains the external
;; Bleve index, but it does not provide interactive search functionality inside
;; Emacs.

;;; Code:

(require 'cl-lib)
(require 'subr-x)

(defgroup org-bleve-index nil
  "Keep an external Bleve index up to date for Org files."
  :group 'org)

(defcustom org-bleve-index-command "org-search"
  "Command used to launch the org-search binary."
  :type 'string
  :group 'org-bleve-index)

(defcustom org-bleve-index-config-file nil
  "Optional txtpb config file passed to org-search.

Nil leaves config discovery to org-search, which uses its default XDG config
location."
  :type '(choice (const :tag "Use org-search default config discovery" nil)
                 (file :must-match nil))
  :group 'org-bleve-index)

(defcustom org-bleve-index-auto-sync t
  "Whether `org-bleve-index-mode' should update the index after Org saves."
  :type 'boolean
  :group 'org-bleve-index)

(defcustom org-bleve-index-diagnostics-buffer "*org-bleve-index*"
  "Buffer used to collect diagnostics for failed org-search commands."
  :type 'string
  :group 'org-bleve-index)

(defvar org-bleve-index--inflight-update-processes (make-hash-table :test #'equal)
  "Map file paths to the current in-flight update process for that path.")

(defvar org-bleve-index--pending-update-requests (make-hash-table :test #'equal)
  "Map file paths to one queued update request for that path.

Each value is a plist with the key `:silent-success'.")

(define-minor-mode org-bleve-index-mode
  "Keep an external Bleve index up to date for file-backed Org buffers.

When enabled, Org buffers install a buffer-local `after-save-hook' that calls
`org-search update-file --json ...' asynchronously."
  :global t
  :lighter " OrgBleve"
  (if org-bleve-index-mode
      (progn
        (add-hook 'org-mode-hook #'org-bleve-index--enable-current-buffer)
        (org-bleve-index--enable-existing-org-buffers))
    (remove-hook 'org-mode-hook #'org-bleve-index--enable-current-buffer)
    (org-bleve-index--disable-existing-buffer-hooks)))

(defun org-bleve-index-open-diagnostics ()
  "Display the diagnostics buffer used for failed org-search commands."
  (interactive)
  (pop-to-buffer (org-bleve-index--diagnostics-buffer)))

(defun org-bleve-index-update-buffer (&optional silent-success)
  "Update the external index for the current file-backed Org buffer.

With SILENT-SUCCESS non-nil, suppress any success minibuffer message. When an
update for the same file is already running, queue one rerun instead of starting
another concurrent process."
  (interactive)
  (org-bleve-index--ensure-org-file-buffer)
  (org-bleve-index--request-update
   (expand-file-name buffer-file-name)
   silent-success))

(defun org-bleve-index-rebuild (&optional silent-success)
  "Rebuild the external Org Bleve index.

With SILENT-SUCCESS non-nil, suppress the success minibuffer message."
  (interactive)
  (org-bleve-index--start-command "rebuild" nil silent-success))

(defun org-bleve-index--enable-current-buffer ()
  (add-hook 'after-save-hook #'org-bleve-index--after-save nil t))

(defun org-bleve-index--enable-existing-org-buffers ()
  (dolist (buffer (buffer-list))
    (with-current-buffer buffer
      (when (derived-mode-p 'org-mode)
        (org-bleve-index--enable-current-buffer)))))

(defun org-bleve-index--disable-existing-buffer-hooks ()
  (dolist (buffer (buffer-list))
    (with-current-buffer buffer
      (remove-hook 'after-save-hook #'org-bleve-index--after-save t))))

(defun org-bleve-index--after-save ()
  (when (and org-bleve-index-mode
             org-bleve-index-auto-sync
             buffer-file-name)
    (org-bleve-index--request-update
     (expand-file-name buffer-file-name)
     t)))

(defun org-bleve-index--ensure-org-file-buffer ()
  (unless (derived-mode-p 'org-mode)
    (user-error "org-bleve-index requires an Org buffer"))
  (unless buffer-file-name
    (user-error "org-bleve-index requires a file-backed buffer")))

(defun org-bleve-index--request-update (path silent-success)
  (let ((expanded-path (expand-file-name path)))
    (if (gethash expanded-path org-bleve-index--inflight-update-processes)
        (org-bleve-index--queue-update expanded-path silent-success)
      (org-bleve-index--start-update-process expanded-path silent-success))))

(defun org-bleve-index--queue-update (path silent-success)
  (let* ((pending-request (gethash path org-bleve-index--pending-update-requests))
         (pending-silent-success (if pending-request
                                     (and (plist-get pending-request :silent-success)
                                          silent-success)
                                   silent-success)))
    (puthash path
             (list :silent-success pending-silent-success)
             org-bleve-index--pending-update-requests)
    'queued))

(defun org-bleve-index--drain-pending-update (path)
  (when-let ((pending-request (gethash path org-bleve-index--pending-update-requests)))
    (remhash path org-bleve-index--pending-update-requests)
    (org-bleve-index--start-update-process
     path
     (plist-get pending-request :silent-success))))

(defun org-bleve-index--start-update-process (path silent-success)
  (when-let ((process (org-bleve-index--start-command "update-file" (list path) silent-success)))
    (process-put process 'org-bleve-index-file-path path)
    (puthash path process org-bleve-index--inflight-update-processes)
    process))

(defun org-bleve-index--start-command (subcommand arguments silent-success)
  (let* ((args (org-bleve-index--command-args subcommand arguments))
         (process-buffer (generate-new-buffer (format " *org-bleve-index-%s*" subcommand))))
    (condition-case err
        (let ((process (apply #'start-process
                              (format "org-bleve-index-%s" subcommand)
                              process-buffer
                              (org-bleve-index--resolve-command)
                              args)))
          (process-put process 'org-bleve-index-subcommand subcommand)
          (process-put process 'org-bleve-index-args args)
          (process-put process 'org-bleve-index-silent-success silent-success)
          (set-process-query-on-exit-flag process nil)
          (set-process-sentinel process #'org-bleve-index--process-sentinel)
          process)
      (error
       (when (buffer-live-p process-buffer)
         (kill-buffer process-buffer))
       (org-bleve-index--record-start-error subcommand args err)
       (message "org-bleve-index: failed to start %s; see %s"
                subcommand
                org-bleve-index-diagnostics-buffer)
       nil))))

(defun org-bleve-index--resolve-command ()
  (let ((command org-bleve-index-command))
    (cond
     ((file-name-directory command)
      (unless (file-executable-p command)
        (signal 'file-missing
                (list (format "Searching for program: %s" command)
                      "No such file or directory"
                      command)))
      command)
     ((executable-find command))
     (t
      (signal 'file-missing
              (list (format "Searching for program: %s" command)
                    "No such file or directory"
                    command))))))

(defun org-bleve-index--command-args (subcommand arguments)
  (append (list "--json")
          (when org-bleve-index-config-file
            (list "--config" (expand-file-name org-bleve-index-config-file)))
          (list subcommand)
          arguments))

(defun org-bleve-index--process-sentinel (process _event)
  (when (memq (process-status process) '(exit signal))
    (let ((path (process-get process 'org-bleve-index-file-path)))
      (unwind-protect
          (if (and (eq (process-status process) 'exit)
                   (zerop (process-exit-status process)))
              (org-bleve-index--handle-success process)
            (org-bleve-index--handle-failure process))
        (when path
          (when (eq (gethash path org-bleve-index--inflight-update-processes) process)
            (remhash path org-bleve-index--inflight-update-processes))
          (org-bleve-index--drain-pending-update path))
        (when-let ((buffer (process-buffer process)))
          (when (buffer-live-p buffer)
            (kill-buffer buffer)))))))

(defun org-bleve-index--handle-success (process)
  (condition-case err
      (let ((payload (org-bleve-index--parse-json-output (org-bleve-index--process-output process))))
        (org-bleve-index--report-success process payload))
    (error
     (org-bleve-index--record-malformed-response process err)
     (message "org-bleve-index: malformed JSON from %s; see %s"
              (process-get process 'org-bleve-index-subcommand)
              org-bleve-index-diagnostics-buffer))))

(defun org-bleve-index--report-success (process payload)
  (let ((subcommand (process-get process 'org-bleve-index-subcommand))
        (silent-success (process-get process 'org-bleve-index-silent-success)))
    (when (org-bleve-index--should-message-success-p subcommand payload silent-success)
      (message "%s" (org-bleve-index--success-message subcommand payload)))))

(defun org-bleve-index--should-message-success-p (subcommand payload silent-success)
  (cond
   (silent-success nil)
   ((equal subcommand "update-file") nil)
   ((equal subcommand "rebuild") t)
   ((org-bleve-index--update-skip-p payload) nil)
   (t nil)))

(defun org-bleve-index--success-message (subcommand payload)
  (cond
   ((equal subcommand "rebuild")
    (format "org-bleve-index: rebuild finished (%s files, %s entries)"
            (or (org-bleve-index--json-get payload "indexed_file_count") 0)
            (or (org-bleve-index--json-get payload "indexed_entry_count") 0)))
   ((equal subcommand "update-file")
    (format "org-bleve-index: updated %s"
            (or (org-bleve-index--json-get payload "path") "file")))
   (t
    (format "org-bleve-index: %s finished" subcommand))))

(defun org-bleve-index--update-skip-p (payload)
  (equal (org-bleve-index--json-get payload "status") "skipped"))

(defun org-bleve-index--handle-failure (process)
  (condition-case err
      (let* ((output (org-bleve-index--process-output process))
             (payload (org-bleve-index--parse-json-output output)))
        (org-bleve-index--record-json-failure process payload output)
        (message "org-bleve-index: %s; see %s"
                 (org-bleve-index--failure-summary process payload)
                 org-bleve-index-diagnostics-buffer))
    (error
     (org-bleve-index--record-malformed-response process err)
     (message "org-bleve-index: malformed JSON from %s; see %s"
              (process-get process 'org-bleve-index-subcommand)
              org-bleve-index-diagnostics-buffer))))

(defun org-bleve-index--failure-summary (process payload)
  (if (org-bleve-index--json-get payload "duplicates")
      "duplicate Org IDs"
    (format "%s failed" (process-get process 'org-bleve-index-subcommand))))

(defun org-bleve-index--record-json-failure (process payload raw-output)
  (let* ((subcommand (process-get process 'org-bleve-index-subcommand))
         (args (process-get process 'org-bleve-index-args))
         (command-line (org-bleve-index--command-line args)))
    (if-let ((duplicates (org-bleve-index--json-get payload "duplicates")))
        (org-bleve-index--append-diagnostic-entry
         (format "%s: duplicate Org IDs" subcommand)
         command-line
         (append (list (format "Summary: %s"
                               (or (org-bleve-index--json-get payload "error")
                                   "found duplicate Org IDs"))
                       "Duplicates:")
                 (org-bleve-index--duplicate-diagnostic-lines duplicates))
         raw-output)
      (org-bleve-index--append-diagnostic-entry
       (format "%s: command failed" subcommand)
       command-line
       (list (format "Error: %s"
                     (or (org-bleve-index--json-get payload "error")
                         "unknown org-search error")))
       raw-output))))

(defun org-bleve-index--duplicate-diagnostic-lines (duplicates)
  (let (lines)
    (dolist (duplicate duplicates)
      (setq lines
            (append lines
                    (list (format "- %s"
                                  (or (org-bleve-index--json-get duplicate "id")
                                      "<unknown-id>")))))
      (dolist (occurrence (or (org-bleve-index--json-get duplicate "occurrences") '()))
        (let ((path (or (org-bleve-index--json-get occurrence "path") "<unknown-path>"))
              (headline (org-bleve-index--json-get occurrence "headline")))
          (setq lines
                (append lines
                        (list (if (and headline (not (string-empty-p headline)))
                                  (format "  - %s: %s" path headline)
                                (format "  - %s" path))))))))
    lines))

(defun org-bleve-index--record-start-error (subcommand args err)
  (org-bleve-index--append-diagnostic-entry
   (format "%s: failed to start" subcommand)
   (org-bleve-index--command-line args)
   (list (format "Error: %s" (error-message-string err)))))

(defun org-bleve-index--record-malformed-response (process err)
  (let ((subcommand (process-get process 'org-bleve-index-subcommand))
        (args (process-get process 'org-bleve-index-args))
        (output (org-bleve-index--process-output process)))
    (org-bleve-index--append-diagnostic-entry
     (format "%s: malformed JSON response" subcommand)
     (org-bleve-index--command-line args)
     (list (format "Error: %s" (error-message-string err)))
     output)))

(defun org-bleve-index--append-diagnostic-entry (title command-line lines &optional raw-output)
  (with-current-buffer (org-bleve-index--diagnostics-buffer)
    (let ((inhibit-read-only t))
      (goto-char (point-max))
      (unless (bobp)
        (insert "\n"))
      (insert (format-time-string "[%Y-%m-%d %H:%M:%S] "))
      (insert title "\n")
      (insert (format "Command: %s\n" command-line))
      (dolist (line lines)
        (insert line "\n"))
      (when raw-output
        (insert "Raw output:\n")
        (insert raw-output)
        (unless (string-suffix-p "\n" raw-output)
          (insert "\n"))))))

(defun org-bleve-index--diagnostics-buffer ()
  (let ((buffer (get-buffer-create org-bleve-index-diagnostics-buffer)))
    (with-current-buffer buffer
      (unless (derived-mode-p 'special-mode)
        (special-mode)))
    buffer))

(defun org-bleve-index--command-line (args)
  (string-join (cons org-bleve-index-command args) " "))

(defun org-bleve-index--parse-json-output (output)
  (let ((trimmed (string-trim output)))
    (when (string-empty-p trimmed)
      (error "empty JSON response"))
    (json-parse-string trimmed
                       :object-type 'hash-table
                       :array-type 'list
                       :null-object nil
                       :false-object nil)))

(defun org-bleve-index--json-get (object key)
  (and object
       (gethash key object)))

(defun org-bleve-index--process-output (process)
  (if-let ((buffer (process-buffer process)))
      (with-current-buffer buffer
        (buffer-substring-no-properties (point-min) (point-max)))
    ""))

(provide 'org-bleve-index)

;;; org-bleve-index.el ends here
