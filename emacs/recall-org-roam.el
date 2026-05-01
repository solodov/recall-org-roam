;;; recall-org-roam.el --- Keep an external Bleve index up to date for Org files -*- lexical-binding: t; -*-

;; Copyright (C) 2026 Peter Solodov

;; Author: Peter Solodov
;; Keywords: outlines, tools
;; Package-Requires: ((emacs "28.1"))
;; Version: 0.1.0

;;; Commentary:

;; recall-org-roam keeps an external Bleve index maintained by the `recall-org-roam`
;; binary in sync with file-backed Org buffers.
;;
;; To integrate it into Emacs:
;;
;; 1. Install the `recall-org-roam` binary and make it available on PATH, or set
;;    `recall-org-roam-command` to the absolute binary path.
;; 2. Put your recall-org-roam txtpb config in the default XDG location, or set
;;    `recall-org-roam-config-file` to the config file you want Emacs to use.
;; 3. Load this file and enable `recall-org-roam-mode`.
;;
;; Minimal setup:
;;
;;   (add-to-list 'load-path "/path/to/recall-org-roam/emacs")
;;   (require 'recall-org-roam)
;;   (recall-org-roam-mode 1)
;;
;; With `recall-org-roam-mode` enabled, file-backed Org buffers trigger
;; asynchronous `recall-org-roam update-file --json ...` commands after save. Manual
;; rebuild is available through `recall-org-roam-rebuild`, and the diagnostics
;; buffer can be opened with `recall-org-roam-open-diagnostics`.
;;
;; v1 intentionally focuses on save-time content edits. That covers normal Org
;; edits well, but rename and delete workflows are only partially covered. When
;; you suspect the index may have drifted after file moves or other filesystem
;; lifecycle events, run `recall-org-roam-rebuild` as the repair path.
;;
;; The save-driven contract is still useful in v1: ordinary saves stay
;; incremental, and missing-file cleanup can still happen through the
;; `recall-org-roam` CLI when the relevant path is updated there. Future work can
;; improve rename/delete handling through explicit commands, editor hooks for
;; rename-like operations, or broader filesystem observation.
;;
;; This package intentionally keeps a narrow scope: it maintains the external
;; Bleve index, but it does not provide interactive search functionality inside
;; Emacs.

;;; Code:

(require 'cl-lib)
(require 'subr-x)

(defgroup recall-org-roam nil
  "Keep an external Bleve index up to date for Org files."
  :group 'org)

(defcustom recall-org-roam-command "recall-org-roam"
  "Command used to launch the recall-org-roam binary."
  :type 'string
  :group 'recall-org-roam)

(defcustom recall-org-roam-config-file nil
  "Optional txtpb config file passed to recall-org-roam.

Nil leaves config discovery to recall-org-roam, which uses its default XDG
config location."
  :type '(choice (const :tag "Use recall-org-roam default config discovery" nil)
                 (file :must-match nil))
  :group 'recall-org-roam)

(defcustom recall-org-roam-auto-sync t
  "Whether `recall-org-roam-mode' should update the index after Org saves."
  :type 'boolean
  :group 'recall-org-roam)

(defcustom recall-org-roam-diagnostics-buffer "*recall-org-roam*"
  "Buffer used to collect diagnostics for failed recall-org-roam commands."
  :type 'string
  :group 'recall-org-roam)

(defvar recall-org-roam--inflight-update-processes (make-hash-table :test #'equal)
  "Map file paths to the current in-flight update process for that path.")

(defvar recall-org-roam--pending-update-requests (make-hash-table :test #'equal)
  "Map file paths to one queued update request for that path.

Each value is a plist with the key `:silent-success'.")

(define-minor-mode recall-org-roam-mode
  "Keep an external Bleve index up to date for file-backed Org buffers.

When enabled, Org buffers install a buffer-local `after-save-hook' that calls
`recall-org-roam update-file --json ...' asynchronously."
  :global t
  :lighter nil
  (if recall-org-roam-mode
      (progn
        (add-hook 'org-mode-hook #'recall-org-roam--enable-current-buffer)
        (recall-org-roam--enable-existing-org-buffers))
    (remove-hook 'org-mode-hook #'recall-org-roam--enable-current-buffer)
    (recall-org-roam--disable-existing-buffer-hooks)))

(defun recall-org-roam-open-diagnostics ()
  "Display the diagnostics buffer used for failed recall-org-roam commands."
  (interactive)
  (pop-to-buffer (recall-org-roam--diagnostics-buffer)))

(defun recall-org-roam-update-buffer (&optional silent-success)
  "Update the external index for the current file-backed Org buffer.

With SILENT-SUCCESS non-nil, suppress any success minibuffer message. When an
update for the same file is already running, queue one rerun instead of starting
another concurrent process."
  (interactive)
  (recall-org-roam--ensure-org-file-buffer)
  (recall-org-roam--request-update
   (expand-file-name buffer-file-name)
   silent-success))

(defun recall-org-roam-rebuild (&optional silent-success)
  "Rebuild the external Org Bleve index.

With SILENT-SUCCESS non-nil, suppress the success minibuffer message."
  (interactive)
  (recall-org-roam--start-command "rebuild" nil silent-success))

(defun recall-org-roam--enable-current-buffer ()
  (add-hook 'after-save-hook #'recall-org-roam--after-save nil t))

(defun recall-org-roam--enable-existing-org-buffers ()
  (dolist (buffer (buffer-list))
    (with-current-buffer buffer
      (when (derived-mode-p 'org-mode)
        (recall-org-roam--enable-current-buffer)))))

(defun recall-org-roam--disable-existing-buffer-hooks ()
  (dolist (buffer (buffer-list))
    (with-current-buffer buffer
      (remove-hook 'after-save-hook #'recall-org-roam--after-save t))))

(defun recall-org-roam--after-save ()
  (when (and recall-org-roam-mode
             recall-org-roam-auto-sync
             buffer-file-name)
    (recall-org-roam--request-update
     (expand-file-name buffer-file-name)
     t)))

(defun recall-org-roam--ensure-org-file-buffer ()
  (unless (derived-mode-p 'org-mode)
    (user-error "recall-org-roam requires an Org buffer"))
  (unless buffer-file-name
    (user-error "recall-org-roam requires a file-backed buffer")))

(defun recall-org-roam--request-update (path silent-success)
  (let ((expanded-path (expand-file-name path)))
    (if (gethash expanded-path recall-org-roam--inflight-update-processes)
        (recall-org-roam--queue-update expanded-path silent-success)
      (recall-org-roam--start-update-process expanded-path silent-success))))

(defun recall-org-roam--queue-update (path silent-success)
  (let* ((pending-request (gethash path recall-org-roam--pending-update-requests))
         (pending-silent-success (if pending-request
                                     (and (plist-get pending-request :silent-success)
                                          silent-success)
                                   silent-success)))
    (puthash path
             (list :silent-success pending-silent-success)
             recall-org-roam--pending-update-requests)
    'queued))

(defun recall-org-roam--drain-pending-update (path)
  (when-let ((pending-request (gethash path recall-org-roam--pending-update-requests)))
    (remhash path recall-org-roam--pending-update-requests)
    (recall-org-roam--start-update-process
     path
     (plist-get pending-request :silent-success))))

(defun recall-org-roam--start-update-process (path silent-success)
  (when-let ((process (recall-org-roam--start-command "update-file" (list path) silent-success)))
    (process-put process 'recall-org-roam-file-path path)
    (puthash path process recall-org-roam--inflight-update-processes)
    process))

(defun recall-org-roam--start-command (subcommand arguments silent-success)
  (let* ((args (recall-org-roam--command-args subcommand arguments))
         (process-buffer (generate-new-buffer (format " *recall-org-roam-%s*" subcommand))))
    (condition-case err
        (let ((process (apply #'start-process
                              (format "recall-org-roam-%s" subcommand)
                              process-buffer
                              (recall-org-roam--resolve-command)
                              args)))
          (process-put process 'recall-org-roam-subcommand subcommand)
          (process-put process 'recall-org-roam-args args)
          (process-put process 'recall-org-roam-silent-success silent-success)
          (set-process-query-on-exit-flag process nil)
          (set-process-sentinel process #'recall-org-roam--process-sentinel)
          process)
      (error
       (when (buffer-live-p process-buffer)
         (kill-buffer process-buffer))
       (recall-org-roam--record-start-error subcommand args err)
       (message "recall-org-roam: failed to start %s; see %s"
                subcommand
                recall-org-roam-diagnostics-buffer)
       nil))))

(defun recall-org-roam--resolve-command ()
  (let ((command recall-org-roam-command))
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

(defun recall-org-roam--command-args (subcommand arguments)
  (append (list "--json")
          (when recall-org-roam-config-file
            (list "--config" (expand-file-name recall-org-roam-config-file)))
          (list subcommand)
          arguments))

(defun recall-org-roam--process-sentinel (process _event)
  (when (memq (process-status process) '(exit signal))
    (let ((path (process-get process 'recall-org-roam-file-path)))
      (unwind-protect
          (if (and (eq (process-status process) 'exit)
                   (zerop (process-exit-status process)))
              (recall-org-roam--handle-success process)
            (recall-org-roam--handle-failure process))
        (when path
          (when (eq (gethash path recall-org-roam--inflight-update-processes) process)
            (remhash path recall-org-roam--inflight-update-processes))
          (recall-org-roam--drain-pending-update path))
        (when-let ((buffer (process-buffer process)))
          (when (buffer-live-p buffer)
            (kill-buffer buffer)))))))

(defun recall-org-roam--handle-success (process)
  (condition-case err
      (let ((payload (recall-org-roam--parse-json-output (recall-org-roam--process-output process))))
        (recall-org-roam--report-success process payload))
    (error
     (recall-org-roam--record-malformed-response process err)
     (message "recall-org-roam: malformed JSON from %s; see %s"
              (process-get process 'recall-org-roam-subcommand)
              recall-org-roam-diagnostics-buffer))))

(defun recall-org-roam--report-success (process payload)
  (let ((subcommand (process-get process 'recall-org-roam-subcommand))
        (silent-success (process-get process 'recall-org-roam-silent-success)))
    (when (recall-org-roam--should-message-success-p subcommand payload silent-success)
      (message "%s" (recall-org-roam--success-message subcommand payload)))))

(defun recall-org-roam--should-message-success-p (subcommand payload silent-success)
  (cond
   (silent-success nil)
   ((equal subcommand "update-file") nil)
   ((equal subcommand "rebuild") t)
   ((recall-org-roam--update-skip-p payload) nil)
   (t nil)))

(defun recall-org-roam--success-message (subcommand payload)
  (cond
   ((equal subcommand "rebuild")
    (format "recall-org-roam: rebuild finished (%s files, %s entries)"
            (or (recall-org-roam--json-get payload "indexed_file_count") 0)
            (or (recall-org-roam--json-get payload "indexed_entry_count") 0)))
   ((equal subcommand "update-file")
    (format "recall-org-roam: updated %s"
            (or (recall-org-roam--json-get payload "path") "file")))
   (t
    (format "recall-org-roam: %s finished" subcommand))))

(defun recall-org-roam--update-skip-p (payload)
  (equal (recall-org-roam--json-get payload "status") "skipped"))

(defun recall-org-roam--handle-failure (process)
  (condition-case err
      (let* ((output (recall-org-roam--process-output process))
             (payload (recall-org-roam--parse-json-output output)))
        (recall-org-roam--record-json-failure process payload output)
        (message "recall-org-roam: %s; see %s"
                 (recall-org-roam--failure-summary process payload)
                 recall-org-roam-diagnostics-buffer))
    (error
     (recall-org-roam--record-malformed-response process err)
     (message "recall-org-roam: malformed JSON from %s; see %s"
              (process-get process 'recall-org-roam-subcommand)
              recall-org-roam-diagnostics-buffer))))

(defun recall-org-roam--failure-summary (process payload)
  (if (recall-org-roam--json-get payload "duplicates")
      "duplicate Org IDs"
    (format "%s failed" (process-get process 'recall-org-roam-subcommand))))

(defun recall-org-roam--record-json-failure (process payload raw-output)
  (let* ((subcommand (process-get process 'recall-org-roam-subcommand))
         (args (process-get process 'recall-org-roam-args))
         (command-line (recall-org-roam--command-line args)))
    (if-let ((duplicates (recall-org-roam--json-get payload "duplicates")))
        (recall-org-roam--append-diagnostic-entry
         (format "%s: duplicate Org IDs" subcommand)
         command-line
         (append (list (format "Summary: %s"
                               (or (recall-org-roam--json-get payload "error")
                                   "found duplicate Org IDs"))
                       "Duplicates:")
                 (recall-org-roam--duplicate-diagnostic-lines duplicates))
         raw-output)
      (recall-org-roam--append-diagnostic-entry
       (format "%s: command failed" subcommand)
       command-line
       (list (format "Error: %s"
                     (or (recall-org-roam--json-get payload "error")
                         "unknown recall-org-roam error")))
       raw-output))))

(defun recall-org-roam--duplicate-diagnostic-lines (duplicates)
  (let (lines)
    (dolist (duplicate duplicates)
      (setq lines
            (append lines
                    (list (format "- %s"
                                  (or (recall-org-roam--json-get duplicate "id")
                                      "<unknown-id>")))))
      (dolist (occurrence (or (recall-org-roam--json-get duplicate "occurrences") '()))
        (let ((path (or (recall-org-roam--json-get occurrence "path") "<unknown-path>"))
              (headline (recall-org-roam--json-get occurrence "headline")))
          (setq lines
                (append lines
                        (list (if (and headline (not (string-empty-p headline)))
                                  (format "  - %s: %s" path headline)
                                (format "  - %s" path))))))))
    lines))

(defun recall-org-roam--record-start-error (subcommand args err)
  (recall-org-roam--append-diagnostic-entry
   (format "%s: failed to start" subcommand)
   (recall-org-roam--command-line args)
   (list (format "Error: %s" (error-message-string err)))))

(defun recall-org-roam--record-malformed-response (process err)
  (let ((subcommand (process-get process 'recall-org-roam-subcommand))
        (args (process-get process 'recall-org-roam-args))
        (output (recall-org-roam--process-output process)))
    (recall-org-roam--append-diagnostic-entry
     (format "%s: malformed JSON response" subcommand)
     (recall-org-roam--command-line args)
     (list (format "Error: %s" (error-message-string err)))
     output)))

(defun recall-org-roam--append-diagnostic-entry (title command-line lines &optional raw-output)
  (with-current-buffer (recall-org-roam--diagnostics-buffer)
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

(defun recall-org-roam--diagnostics-buffer ()
  (let ((buffer (get-buffer-create recall-org-roam-diagnostics-buffer)))
    (with-current-buffer buffer
      (unless (derived-mode-p 'special-mode)
        (special-mode)))
    buffer))

(defun recall-org-roam--command-line (args)
  (string-join (cons recall-org-roam-command args) " "))

(defun recall-org-roam--parse-json-output (output)
  (let ((trimmed (string-trim output)))
    (when (string-empty-p trimmed)
      (error "empty JSON response"))
    (json-parse-string trimmed
                       :object-type 'hash-table
                       :array-type 'list
                       :null-object nil
                       :false-object nil)))

(defun recall-org-roam--json-get (object key)
  (and object
       (gethash key object)))

(defun recall-org-roam--process-output (process)
  (if-let ((buffer (process-buffer process)))
      (with-current-buffer buffer
        (buffer-substring-no-properties (point-min) (point-max)))
    ""))

(provide 'recall-org-roam)

;;; recall-org-roam.el ends here
