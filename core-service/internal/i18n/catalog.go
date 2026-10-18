package i18n

// Message keys. Every key needs a catalog entry with both languages
// (enforced by TestCatalogComplete).
var (
	// Authentication and account state.
	KeyUnauthenticated          = Key{"unauthenticated"}
	KeyAuthHeaderMissing        = Key{"auth_header_missing"}
	KeyAuthHeaderInvalid        = Key{"auth_header_invalid"}
	KeyTokenInvalid             = Key{"token_invalid"}
	KeyAccountDeleted           = Key{"account_deleted"}
	KeyAccountStatusUnavailable = Key{"account_status_unavailable"}
	KeyAccountNotFound          = Key{"account_not_found"}
	KeyAccountDeleteFailed      = Key{"account_delete_failed"}

	// Rate limiting.
	KeyRateLimited          = Key{"rate_limited"}
	KeyRateLimitUnavailable = Key{"rate_limit_unavailable"}
	KeySlowDown             = Key{"slow_down"}

	// Generic request and server errors.
	KeyInvalidBody      = Key{"invalid_body"}
	KeyValidationFailed = Key{"validation_failed"}
	KeyInternal         = Key{"internal"}
	KeyAIUnavailable    = Key{"ai_unavailable"}
	KeyRouteNotFound    = Key{"route_not_found"}
	KeyMethodNotAllowed = Key{"method_not_allowed"}
	KeyBodyTooLarge     = Key{"body_too_large"}
	KeyUpgradeRequired  = Key{"upgrade_required"}
	KeyRequestFailed    = Key{"request_failed"}

	// Jobs, files, uploads and WebSocket tickets.
	KeyJobIDRequired       = Key{"job_id_required"}
	KeyJobNotFound         = Key{"job_not_found"}
	KeyJobNotCompleted     = Key{"job_not_completed"}
	KeyJobAlreadyCompleted = Key{"job_already_completed"}
	KeyStemSettingsInvalid = Key{"stem_settings_invalid"}
	KeyFileNotFound        = Key{"file_not_found"}
	KeyInvalidFileURL      = Key{"invalid_file_url"}
	KeyURLSignFailed       = Key{"url_sign_failed"}
	KeyProjectIDRequired   = Key{"project_id_required"}
	KeySectionIDRequired   = Key{"section_id_required"}
	KeyFileRequired        = Key{"file_required"}
	KeyFileTooLarge        = Key{"file_too_large"}
	KeyFileTypeInvalid     = Key{"file_type_invalid"}
	KeyFileOpenFailed      = Key{"file_open_failed"}
	KeyUploadIDsInvalid    = Key{"upload_ids_invalid"}
	KeyVocalIDsRequired    = Key{"vocal_ids_required"}
	KeyVocalIDsInvalid     = Key{"vocal_ids_invalid"}
	KeyTicketIssueFailed   = Key{"ticket_issue_failed"}
	KeyTicketInvalid       = Key{"ticket_invalid"}
	KeyTicketCheckFailed   = Key{"ticket_check_failed"}

	// Social service errors (one per sentinel error).
	KeySocialNotFound         = Key{"social_not_found"}
	KeySocialProfileMissing   = Key{"social_profile_missing"}
	KeySocialUsernameInvalid  = Key{"social_username_invalid"}
	KeySocialUsernameTaken    = Key{"social_username_taken"}
	KeySocialValidation       = Key{"social_validation"}
	KeySocialOwnership        = Key{"social_ownership"}
	KeySocialSourceNotFound   = Key{"social_source_not_found"}
	KeySocialBackingClosed    = Key{"social_backing_closed"}
	KeySocialNoStems          = Key{"social_no_stems"}
	KeySocialBadStatus        = Key{"social_bad_status"}
	KeySocialSelfFollow       = Key{"social_self_follow"}
	KeySocialCommentsDisabled = Key{"social_comments_disabled"}
	KeySocialForbidden        = Key{"social_forbidden"}

	// Credits.
	KeyInsufficientCredits  = Key{"insufficient_credits"}
	KeyPlanRequired         = Key{"plan_required"}
	KeyAdminRequired        = Key{"admin_required"}
	KeyPurchaseInvalid      = Key{"purchase_invalid"}
	KeyPurchaseOwnedByOther = Key{"purchase_owned_by_other"}
	KeyActionIDRequired     = Key{"action_id_required"}
	KeyActionExpired        = Key{"action_expired"}
	KeyStoreUnavailable     = Key{"store_unavailable"}

	// Social field validation (422 dogrulama).
	KeyCursorInvalid             = Key{"cursor_invalid"}
	KeyFeedTabInvalid            = Key{"feed_tab_invalid"}
	KeyProfileFeedTypeInvalid    = Key{"profile_feed_type_invalid"}
	KeyProfileTextNUL            = Key{"profile_text_nul"}
	KeyDisplayNameLength         = Key{"display_name_length"}
	KeyBioLength                 = Key{"bio_length"}
	KeyTextFieldsNUL             = Key{"text_fields_nul"}
	KeyTitleLength               = Key{"title_length"}
	KeyDescriptionLength         = Key{"description_length"}
	KeyDescriptionNUL            = Key{"description_nul"}
	KeyDescriptionInvalidChars   = Key{"description_invalid_chars"}
	KeyVisibilityInvalid         = Key{"visibility_invalid"}
	KeyScaleModeInvalid          = Key{"scale_mode_invalid"}
	KeyTonicEmpty                = Key{"tonic_empty"}
	KeyGenreLength               = Key{"genre_length"}
	KeyDurationPositive          = Key{"duration_positive"}
	KeyBPMRange                  = Key{"bpm_range"}
	KeyMasterURLRequired         = Key{"master_url_required"}
	KeyStructureTooLong          = Key{"structure_too_long"}
	KeyStructureItemInvalid      = Key{"structure_item_invalid"}
	KeyLyricsTooLong             = Key{"lyrics_too_long"}
	KeyLyricsSectionInvalid      = Key{"lyrics_section_invalid"}
	KeyLyricsLineInvalid         = Key{"lyrics_line_invalid"}
	KeyStemRequired              = Key{"stem_required"}
	KeyTooManyStems              = Key{"too_many_stems"}
	KeyStemIDInvalid             = Key{"stem_id_invalid"}
	KeyStemIDDuplicate           = Key{"stem_id_duplicate"}
	KeyStemFieldsInvalid         = Key{"stem_fields_invalid"}
	KeyFieldFileURLInvalid       = Key{"field_file_url_invalid"}
	KeyFieldFileNotPrivate       = Key{"field_file_not_private"}
	KeyCommentTextNUL            = Key{"comment_text_nul"}
	KeyCommentTextLength         = Key{"comment_text_length"}
	KeyCommentTimeNegative       = Key{"comment_time_negative"}
	KeyCommentTimeTooLarge       = Key{"comment_time_too_large"}
	KeyCommentTimeBeyondDuration = Key{"comment_time_beyond_duration"}
	KeyReportTargetInvalid       = Key{"report_target_invalid"}
	KeyReportReasonInvalid       = Key{"report_reason_invalid"}
)

// texts holds one message in both languages. Verbs (%s) must match.
type texts struct{ tr, en string }

var catalog = map[Key]texts{
	KeyUnauthenticated:          {"Kimlik doğrulanamadı.", "Authentication failed."},
	KeyAuthHeaderMissing:        {"Authorization başlığı eksik.", "Missing Authorization header."},
	KeyAuthHeaderInvalid:        {"Authorization başlığı geçersiz; beklenen biçim: Bearer <jeton>.", "Invalid Authorization header; expected format: Bearer <token>."},
	KeyTokenInvalid:             {"Erişim jetonu geçersiz ya da süresi dolmuş.", "The access token is invalid or expired."},
	KeyAccountDeleted:           {"Bu hesap silindi.", "This account has been deleted."},
	KeyAccountStatusUnavailable: {"Hesap durumu denetlenemedi, lütfen biraz sonra tekrar deneyin.", "Could not check the account status; please try again shortly."},
	KeyAccountNotFound:          {"Hesap bulunamadı.", "Account not found."},
	KeyAccountDeleteFailed:      {"Hesap silinemedi, lütfen tekrar deneyin.", "Could not delete the account; please try again."},

	KeyRateLimited:          {"Çok fazla istek gönderildi, lütfen biraz bekleyin.", "Too many requests; please wait a moment."},
	KeyRateLimitUnavailable: {"İstek sınırı denetlenemedi, lütfen biraz sonra tekrar deneyin.", "Could not check the rate limit; please try again shortly."},
	KeySlowDown:             {"Biraz yavaşla.", "Slow down a little."},

	KeyInvalidBody:      {"Geçersiz istek gövdesi.", "Invalid request body."},
	KeyValidationFailed: {"Doğrulama başarısız.", "Validation failed."},
	KeyInternal:         {"İşlem tamamlanamadı, lütfen tekrar deneyin.", "The operation could not be completed; please try again."},
	KeyAIUnavailable:    {"Yapay zekâ servisi şu anda yanıt veremiyor, lütfen tekrar deneyin.", "The AI service could not respond; please try again."},
	KeyRouteNotFound:    {"İstenen adres bulunamadı.", "The requested endpoint was not found."},
	KeyMethodNotAllowed: {"Bu adres bu HTTP yöntemini desteklemiyor.", "This endpoint does not support this HTTP method."},
	KeyBodyTooLarge:     {"İstek gövdesi çok büyük.", "The request body is too large."},
	KeyUpgradeRequired:  {"Bu adres bir WebSocket bağlantısı gerektirir.", "This endpoint requires a WebSocket connection."},
	KeyRequestFailed:    {"İstek işlenemedi.", "The request could not be processed."},

	KeyJobIDRequired:       {"İş kimliği (jobId) zorunludur.", "Job ID is required."},
	KeyJobNotFound:         {"İş bulunamadı.", "Job not found."},
	KeyJobNotCompleted:     {"İş henüz tamamlanmadı.", "The job has not completed yet."},
	KeyJobAlreadyCompleted: {"İş zaten tamamlandı.", "The job has already completed."},
	KeyStemSettingsInvalid: {"Geçersiz stem ayarları: mod, odak enstrümanı ve enstrüman listesini kontrol edin.", "Invalid stem settings: check mode, focusInstrument and instrument list."},
	KeyFileNotFound:        {"Dosya bulunamadı.", "File not found."},
	KeyInvalidFileURL:      {"Geçersiz dosya adresi.", "Invalid file URL."},
	KeyURLSignFailed:       {"Dosya adresi üretilemedi.", "Could not generate the file URL."},
	KeyProjectIDRequired:   {"projectId zorunludur.", "projectId is required."},
	KeySectionIDRequired:   {"sectionId zorunludur.", "sectionId is required."},
	KeyFileRequired:        {"Dosya zorunludur.", "A file is required."},
	KeyFileTooLarge:        {"Dosya boyutu 50 MB sınırını aşıyor.", "The file exceeds the 50 MB limit."},
	KeyFileTypeInvalid:     {"Geçersiz dosya türü. Desteklenenler: WAV, M4A, MP3, AAC.", "Invalid file type. Supported: WAV, M4A, MP3, AAC."},
	KeyFileOpenFailed:      {"Dosya açılamadı.", "Could not open the file."},
	KeyUploadIDsInvalid:    {"Geçersiz projectId ya da sectionId.", "Invalid projectId or sectionId."},
	KeyVocalIDsRequired:    {"takeId, projectId ve sectionId zorunludur.", "takeId, projectId and sectionId are required."},
	KeyVocalIDsInvalid:     {"Geçersiz takeId, projectId ya da sectionId.", "Invalid takeId, projectId or sectionId."},
	KeyTicketIssueFailed:   {"Bilet üretilemedi.", "Could not issue a ticket."},
	KeyTicketInvalid:       {"Geçersiz WebSocket bileti.", "Invalid WebSocket ticket."},
	KeyTicketCheckFailed:   {"Bilet denetlenemedi.", "Could not verify the ticket."},

	KeySocialNotFound:         {"Aranan içerik bulunamadı.", "The requested item was not found."},
	KeySocialProfileMissing:   {"Profil henüz oluşturulmamış.", "The profile has not been created yet."},
	KeySocialUsernameInvalid:  {"Geçersiz kullanıcı adı.", "Invalid username."},
	KeySocialUsernameTaken:    {"Bu kullanıcı adı alınmış.", "This username is already taken."},
	KeySocialValidation:       {"İstek doğrulanamadı.", "The request is invalid."},
	KeySocialOwnership:        {"Dosya bu kullanıcıya ait değil.", "The file does not belong to this user."},
	KeySocialSourceNotFound:   {"Kaynak yayın bulunamadı.", "The source publication was not found."},
	KeySocialBackingClosed:    {"Kaynak yayının altyapısı paylaşıma kapalı.", "The source publication's backing track is not shared."},
	KeySocialNoStems:          {"Yayının stem kopyası yok.", "The publication has no stem copies."},
	KeySocialBadStatus:        {"Yayın bu işlem için uygun durumda değil.", "The publication is not in a state that allows this action."},
	KeySocialSelfFollow:       {"Kendini takip edemezsin.", "You cannot follow yourself."},
	KeySocialCommentsDisabled: {"Bu yayının yorumları kapalı.", "Comments are turned off for this publication."},
	KeySocialForbidden:        {"Bu işlem için yetkin yok.", "You are not allowed to do this."},

	KeyInsufficientCredits:  {"Yeterli krediniz yok.", "You do not have enough credits."},
	KeyPlanRequired:         {"Kanallar render'ı Pro planı gerektirir.", "The channels render needs the Pro plan."},
	KeyAdminRequired:        {"Bu alan yalnızca yöneticilere açıktır.", "This area is for administrators only."},
	KeyPurchaseInvalid:      {"Satın alma doğrulanamadı.", "The purchase could not be verified."},
	KeyPurchaseOwnedByOther: {"Bu satın alma başka bir hesaba ait.", "This purchase belongs to another account."},
	KeyActionIDRequired:     {"X-Action-Id başlığı zorunludur.", "The X-Action-Id header is required."},
	KeyActionExpired:        {"Bu işlemin süresi doldu; yeniden başlatın.", "This action has expired; start it again."},
	KeyStoreUnavailable:     {"Mağaza şu anda yanıt vermiyor, lütfen tekrar deneyin.", "The store is not responding right now; please try again."},

	KeyCursorInvalid:             {"İmleç geçersiz.", "Invalid cursor."},
	KeyFeedTabInvalid:            {"sekme senin-icin, takip ya da yeni olmalı.", "sekme must be senin-icin, takip or yeni."},
	KeyProfileFeedTypeInvalid:    {"tur sarkilar ya da coverlar olmalı.", "tur must be sarkilar or coverlar."},
	KeyProfileTextNUL:            {"Görünen ad ve biyografi NUL karakteri içeremez.", "Display name and bio cannot contain NUL characters."},
	KeyDisplayNameLength:         {"Görünen ad 1-40 karakter olmalı.", "Display name must be 1-40 characters."},
	KeyBioLength:                 {"Biyografi en çok 160 karakter olabilir.", "Bio can be at most 160 characters."},
	KeyTextFieldsNUL:             {"Metin alanları NUL karakteri içeremez.", "Text fields cannot contain NUL characters."},
	KeyTitleLength:               {"Başlık 1-80 karakter olmalı.", "Title must be 1-80 characters."},
	KeyDescriptionLength:         {"Açıklama en çok 300 karakter olabilir.", "Description can be at most 300 characters."},
	KeyDescriptionNUL:            {"Açıklama NUL karakteri içeremez.", "Description cannot contain NUL characters."},
	KeyDescriptionInvalidChars:   {"Açıklama geçersiz karakter içeriyor.", "Description contains invalid characters."},
	KeyVisibilityInvalid:         {"Görünürlük herkes, takipciler ya da link olmalı.", "Visibility must be herkes, takipciler or link."},
	KeyScaleModeInvalid:          {"ton.mod major ya da minor olmalı.", "ton.mod must be major or minor."},
	KeyTonicEmpty:                {"ton.tonik boş olamaz.", "ton.tonik cannot be empty."},
	KeyGenreLength:               {"Tür 1-40 karakter olmalı.", "Genre must be 1-40 characters."},
	KeyDurationPositive:          {"sureSn sıfırdan büyük olmalı.", "sureSn must be greater than zero."},
	KeyBPMRange:                  {"BPM 20-300 aralığında olmalı.", "BPM must be between 20 and 300."},
	KeyMasterURLRequired:         {"masterUrl zorunludur.", "masterUrl is required."},
	KeyStructureTooLong:          {"Şarkı yapısı çok uzun.", "The song structure is too long."},
	KeyStructureItemInvalid:      {"Şarkı yapısında geçersiz bir öğe var.", "The song structure contains an invalid item."},
	KeyLyricsTooLong:             {"Sözler çok uzun.", "The lyrics are too long."},
	KeyLyricsSectionInvalid:      {"Söz bölümü geçersiz.", "Invalid lyrics section."},
	KeyLyricsLineInvalid:         {"Söz satırı geçersiz.", "Invalid lyrics line."},
	KeyStemRequired:              {"Altyapı paylaşımı açıkken en az bir stem gerekli.", "At least one stem is required when the backing track is shared."},
	KeyTooManyStems:              {"Çok fazla stem var.", "Too many stems."},
	KeyStemIDInvalid:             {"Stem kimliği geçersiz.", "Invalid stem ID."},
	KeyStemIDDuplicate:           {"Stem kimliği tekrar ediyor.", "Duplicate stem ID."},
	KeyStemFieldsInvalid:         {"Stem alanları geçersiz.", "Invalid stem fields."},
	KeyFieldFileURLInvalid:       {"%s: geçersiz dosya adresi.", "%s: invalid file URL."},
	KeyFieldFileNotPrivate:       {"%s: paylaşılmamış (özel) bir dosya olmalı.", "%s: must be a private (unshared) file."},
	KeyCommentTextNUL:            {"Yorum metni NUL karakteri içeremez.", "Comment text cannot contain NUL characters."},
	KeyCommentTextLength:         {"Yorum metni 1-500 karakter olmalı.", "Comment text must be 1-500 characters."},
	KeyCommentTimeNegative:       {"zamanMs negatif olamaz.", "zamanMs cannot be negative."},
	KeyCommentTimeTooLarge:       {"zamanMs çok büyük.", "zamanMs is too large."},
	KeyCommentTimeBeyondDuration: {"zamanMs şarkının süresini aşıyor.", "zamanMs exceeds the song's duration."},
	KeyReportTargetInvalid:       {"hedefTuru yayin ya da yorum olmalı.", "hedefTuru must be yayin or yorum."},
	KeyReportReasonInvalid:       {"neden spam, taciz, telif, uygunsuz ya da diger olmalı.", "neden must be spam, taciz, telif, uygunsuz or diger."},
}
