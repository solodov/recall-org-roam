;;; org-recall-index.el --- Keep an external Bleve index up to date for Org files -*- lexical-binding: t; -*-

;; Copyright (C) 2026 Peter Solodov

;; Author: Peter Solodov
;; Keywords: outlines, tools
;; Package-Requires: ((emacs "28.1"))
;; Version: 0.1.0

;;; Commentary:

;; org-recall-index keeps an external Bleve index maintained by the `org-recall-index`
;; binary in sync with file-backed Org buffers.
;;
;; To integrate it into Emacs:
;;
;; 1. Install the `org-recall-index` binary and make it available on PATH, or set
;;    `org-recall-index-command` to the absolute binary path.
;; 2. Put your org-recall-index txtpb config in the default XDG location, or set
;;    `org-recall-index-config-file` to the config file you want Emacs to use.
;; 3. Load this file and enable `org-recall-index-mode`.
;;
;; Minimal setup:
;;
;;   (add-to-list 'load-path "/path/to/org-recall-index/emacs")
;;   (require 'org-recall-index)
;;   (org-recall-index-mode 1)
;;
;; With `org-recall-index-mode` enabled, file-backed Org buffers trigger
;; asynchronous `org-recall-index update-file --json ...` commands after save. Manual
;; rebuild is available through `org-recall-index-rebuild`, and the diagnostics
;; buffer can be opened with `org-recall-index-open-diagnostics`.
;;
;; v1 intentionally focuses on save-time content edits. That covers normal Org
;; edits well, but rename and delete workflows are only partially covered. When
;; you suspect the index may have drifted after file moves or other filesystem
;; lifecycle events, run `org-recall-index-rebuild` as the repair path.
;;
;; The save-driven contract is still useful in v1: ordinary saves stay
;; incremental, and missing-file cleanup can still happen through the
;; `org-recall-index` CLI when the relevant path is updated there. Future work can
;; improve rename/delete handling through explicit commands, editor hooks for
;; rename-like operations, or broader filesystem observation.
;;
;; This package intentionally keeps a narrow scope: it maintains the external
;; Bleve index, but it does not provide interactive search functionality inside
;; Emacs.

;;; Code:

(require 'cl-lib)
(require 'subr-x)

(defgroup org-recall-index nil
  "Keep an external Bleve index up to date for Org files."
  :group 'org)

(defcustom org-recall-index-command "org-recall-index"
  "Command used to launch the org-recall-index binary."
  :type 'string
  :group 'org-recall-index)

(defcustom org-recall-index-config-file nil
  "Optional txtpb config file passed to org-recall-index.

Nil leaves config discovery to org-recall-index, which uses its default XDG config
location."
  :type '(choice (const :tag "Use org-recall-index default config discovery" nil)
                 (file :must-match nil))
  :group 'org-recall-index)

(defcustom org-recall-index-auto-sync t
  "Whether `org-recall-index-mode' should update the index after Org saves."
  :type 'boolean
  :group 'org-recall-index)

(defcustom org-recall-index-diagnostics-buffer "*org-recall-index*"
  "Buffer used to collect diagnostics for failed org-recall-index commands."
  :type 'string
  :group 'org-recall-index)

(defvar org-recall-index--inflight-update-processes (make-hash-table :test #'equal)
  "Map file paths to the current in-flight update process for that path.")

(defvar org-recall-index--pending-update-requests (make-hash-table :test #'equal)
  "Map file paths to one queued update request for that path.

Each value is a plist with the key `:silent-success'.")

(define-minor-mode org-recall-index-mode
  "Keep an external Bleve index up to date for file-backed Org buffers.

When enabled, Org buffers install a buffer-local `after-save-hook' that calls
`org-recall-index update-file --json ...' asynchronously."
  :global t
  :lighter " OrgRecall"
  (if org-recall-index-mode
      (progn
        (add-hook 'org-mode-hook #'org-recall-index--enable-current-buffer)
        (org-recall-index--enable-existing-org-buffers))
    (remove-hook 'org-mode-hook #'org-recall-index--enable-current-buffer)
    (org-recall-index--disable-existing-buffer-hooks)))

(defun org-recall-index-open-diagnostics ()
  "Display the diagnostics buffer used for failed org-recall-index commands."
  (interactive)
  (pop-to-buffer (org-recall-index--diagnostics-buffer)))

(defun org-recall-index-update-buffer (&optional silent-success)
  "Update the external index for the current file-backed Org buffer.

With SILENT-SUCCESS non-nil, suppress any success minibuffer message. When an
update for the same file is already running, queue one rerun instead of starting
another concurrent process."
  (interactive)
  (org-recall-index--ensure-org-file-buffer)
  (org-recall-index--request-update
   (expand-file-name buffer-file-name)
   silent-success))

(defun org-recall-index-rebuild (&optional silent-success)
  "Rebuild the external Org Bleve index.

With SILENT-SUCCESS non-nil, suppress the success minibuffer message."
  (interactive)
  (org-recall-index--start-command "rebuild" nil silent-success))

(defun org-recall-index--enable-current-buffer ()
  (add-hook 'after-save-hook #'org-recall-index--after-save nil t))

(defun org-recall-index--enable-existing-org-buffers ()
  (dolist (buffer (buffer-list))
    (with-current-buffer buffer
      (when (derived-mode-p 'org-mode)
        (org-recall-index--enable-current-buffer)))))

(defun org-recall-index--disable-existing-buffer-hooks ()
  (dolist (buffer (buffer-list))
    (with-current-buffer buffer
      (remove-hook 'after-save-hook #'org-recall-index--after-save t))))

(defun org-recall-index--after-save ()
  (when (and org-recall-index-mode
             org-recall-index-auto-sync
             buffer-file-name)
    (org-recall-index--request-update
     (expand-file-name buffer-file-name)
     t)))

(defun org-recall-index--ensure-org-file-buffer ()
  (unless (derived-mode-p 'org-mode)
    (user-error "org-recall-index requires an Org buffer"))
  (unless buffer-file-name
    (user-error "org-recall-index requires a file-backed buffer")))

(defun org-recall-index--request-update (path silent-success)
  (let ((expanded-path (expand-file-name path)))
    (if (gethash expanded-path org-recall-index--inflight-update-processes)
        (org-recall-index--queue-update expanded-path silent-success)
      (org-recall-index--start-update-process expanded-path silent-success))))

(defun org-recall-index--queue-update (path silent-success)
  (let* ((pending-request (gethash path org-recall-index--pending-update-requests))
         (pending-silent-success (if pending-request
                                     (and (plist-get pending-request :silent-success)
                                          silent-success)
                                   silent-success)))
    (puthash path
             (list :silent-success pending-silent-success)
             org-recall-index--pending-update-requests)
    'queued))

(defun org-recall-index--drain-pending-update (path)
  (when-let ((pending-request (gethash path org-recall-index--pending-update-requests)))
    (remhash path org-recall-index--pending-update-requests)
    (org-recall-index--start-update-process
     path
     (plist-get pending-request :silent-success))))

(defun org-recall-index--start-update-process (path silent-success)
  (when-let ((process (org-recall-index--start-command "update-file" (list path) silent-success)))
    (process-put process 'org-recall-index-file-path path)
    (puthash path process org-recall-index--inflight-update-processes)
    process))

(defun org-recall-index--start-command (subcommand arguments silent-success)
  (let* ((args (org-recall-index--command-args subcommand arguments))
         (process-buffer (generate-new-buffer (format " *org-recall-index-%s*" subcommand))))
    (condition-case err
        (let ((process (apply #'start-process
                              (format "org-recall-index-%s" subcommand)
                              process-buffer
                              (org-recall-index--resolve-command)
                              args)))
          (process-put process 'org-recall-index-subcommand subcommand)
          (process-put process 'org-recall-index-args args)
          (process-put process 'org-recall-index-silent-success silent-success)
          (set-process-query-on-exit-flag process nil)
          (set-process-sentinel process #'org-recall-index--process-sentinel)
          process)
      (error
       (when (buffer-live-p process-buffer)
         (kill-buffer process-buffer))
       (org-recall-index--record-start-error subcommand args err)
       (message "org-recall-index: failed to start %s; see %s"
                subcommand
                org-recall-index-diagnostics-buffer)
       nil))))

(defun org-recall-index--resolve-command ()
  (let ((command org-recall-index-command))
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

(defun org-recall-index--command-args (subcommand arguments)
  (append (list "--json")
          (when org-recall-index-config-file
            (list "--config" (expand-file-name org-recall-index-config-file)))
          (list subcommand)
          arguments))

(defun org-recall-index--process-sentinel (process _event)
  (when (memq (process-status process) '(exit signal))
    (let ((path (process-get process 'org-recall-index-file-path)))
      (unwind-protect
          (if (and (eq (process-status process) 'exit)
                   (zerop (process-exit-status process)))
              (org-recall-index--handle-success process)
            (org-recall-index--handle-failure process))
        (when path
          (when (eq (gethash path org-recall-index--inflight-update-processes) process)
            (remhash path org-recall-index--inflight-update-processes))
          (org-recall-index--drain-pending-update path))
        (when-let ((buffer (process-buffer process)))
          (when (buffer-live-p buffer)
            (kill-buffer buffer)))))))

(defun org-recall-index--handle-success (process)
  (condition-case err
      (let ((payload (org-recall-index--parse-json-output (org-recall-index--process-output process))))
        (org-recall-index--report-success process payload))
    (error
     (org-recall-index--record-malformed-response process err)
     (message "org-recall-index: malformed JSON from %s; see %s"
              (process-get process 'org-recall-index-subcommand)
              org-recall-index-diagnostics-buffer))))

(defun org-recall-index--report-success (process payload)
  (let ((subcommand (process-get process 'org-recall-index-subcommand))
        (silent-success (process-get process 'org-recall-index-silent-success)))
    (when (org-recall-index--should-message-success-p subcommand payload silent-success)
      (message "%s" (org-recall-index--success-message subcommand payload)))))

(defun org-recall-index--should-message-success-p (subcommand payload silent-success)
  (cond
   (silent-success nil)
   ((equal subcommand "update-file") nil)
   ((equal subcommand "rebuild") t)
   ((org-recall-index--update-skip-p payload) nil)
   (t nil)))

(defun org-recall-index--success-message (subcommand payload)
  (cond
   ((equal subcommand "rebuild")
    (format "org-recall-index: rebuild finished (%s files, %s entries)"
            (or (org-recall-index--json-get payload "indexed_file_count") 0)
            (or (org-recall-index--json-get payload "indexed_entry_count") 0)))
   ((equal subcommand "update-file")
    (format "org-recall-index: updated %s"
            (or (org-recall-index--json-get payload "path") "file")))
   (t
    (format "org-recall-index: %s finished" subcommand))))

(defun org-recall-index--update-skip-p (payload)
  (equal (org-recall-index--json-get payload "status") "skipped"))

(defun org-recall-index--handle-failure (process)
  (condition-case err
      (let* ((output (org-recall-index--process-output process))
             (payload (org-recall-index--parse-json-output output)))
        (org-recall-index--record-json-failure process payload output)
        (message "org-recall-index: %s; see %s"
                 (org-recall-index--failure-summary process payload)
                 org-recall-index-diagnostics-buffer))
    (error
     (org-recall-index--record-malformed-response process err)
     (message "org-recall-index: malformed JSON from %s; see %s"
              (process-get process 'org-recall-index-subcommand)
              org-recall-index-diagnostics-buffer))))

(defun org-recall-index--failure-summary (process payload)
  (if (org-recall-index--json-get payload "duplicates")
      "duplicate Org IDs"
    (format "%s failed" (process-get process 'org-recall-index-subcommand))))

(defun org-recall-index--record-json-failure (process payload raw-output)
  (let* ((subcommand (process-get process 'org-recall-index-subcommand))
         (args (process-get process 'org-recall-index-args))
         (command-line (org-recall-index--command-line args)))
    (if-let ((duplicates (org-recall-index--json-get payload "duplicates")))
        (org-recall-index--append-diagnostic-entry
         (format "%s: duplicate Org IDs" subcommand)
         command-line
         (append (list (format "Summary: %s"
                               (or (org-recall-index--json-get payload "error")
                                   "found duplicate Org IDs"))
                       "Duplicates:")
                 (org-recall-index--duplicate-diagnostic-lines duplicates))
         raw-output)
      (org-recall-index--append-diagnostic-entry
       (format "%s: command failed" subcommand)
       command-line
       (list (format "Error: %s"
                     (or (org-recall-index--json-get payload "error")
                         "unknown org-recall-index error")))
       raw-output))))

(defun org-recall-index--duplicate-diagnostic-lines (duplicates)
  (let (lines)
    (dolist (duplicate duplicates)
      (setq lines
            (append lines
                    (list (format "- %s"
                                  (or (org-recall-index--json-get duplicate "id")
                                      "<unknown-id>")))))
      (dolist (occurrence (or (org-recall-index--json-get duplicate "occurrences") '()))
        (let ((path (or (org-recall-index--json-get occurrence "path") "<unknown-path>"))
              (headline (org-recall-index--json-get occurrence "headline")))
          (setq lines
                (append lines
                        (list (if (and headline (not (string-empty-p headline)))
                                  (format "  - %s: %s" path headline)
                                (format "  - %s" path))))))))
    lines))

(defun org-recall-index--record-start-error (subcommand args err)
  (org-recall-index--append-diagnostic-entry
   (format "%s: failed to start" subcommand)
   (org-recall-index--command-line args)
   (list (format "Error: %s" (error-message-string err)))))

(defun org-recall-index--record-malformed-response (process err)
  (let ((subcommand (process-get process 'org-recall-index-subcommand))
        (args (process-get process 'org-recall-index-args))
        (output (org-recall-index--process-output process)))
    (org-recall-index--append-diagnostic-entry
     (format "%s: malformed JSON response" subcommand)
     (org-recall-index--command-line args)
     (list (format "Error: %s" (error-message-string err)))
     output)))

(defun org-recall-index--append-diagnostic-entry (title command-line lines &optional raw-output)
  (with-current-buffer (org-recall-index--diagnostics-buffer)
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

(defun org-recall-index--diagnostics-buffer ()
  (let ((buffer (get-buffer-create org-recall-index-diagnostics-buffer)))
    (with-current-buffer buffer
      (unless (derived-mode-p 'special-mode)
        (special-mode)))
    buffer))

(defun org-recall-index--command-line (args)
  (string-join (cons org-recall-index-command args) " "))

(defun org-recall-index--parse-json-output (output)
  (let ((trimmed (string-trim output)))
    (when (string-empty-p trimmed)
      (error "empty JSON response"))
    (json-parse-string trimmed
                       :object-type 'hash-table
                       :array-type 'list
                       :null-object nil
                       :false-object nil)))

(defun org-recall-index--json-get (object key)
  (and object
       (gethash key object)))

(defun org-recall-index--process-output (process)
  (if-let ((buffer (process-buffer process)))
      (with-current-buffer buffer
        (buffer-substring-no-properties (point-min) (point-max)))
    ""))

(provide 'org-recall-index)

;;; org-recall-index.el ends here
